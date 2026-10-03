package quack

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// validateCaseContextValues validates supplied context against the template's
// fields and returns the stored JSON plus any message links found in
// message-link fields. Context is always optional; an old template marked
// required cannot block moderation. Unknown or duplicate keys are rejected.
func validateCaseContextValues(fields []model.CaseTemplateContextField, inputs []CaseContextValueInput) (string, []string, error) {
	byKey := make(map[string]json.RawMessage, len(inputs))
	for _, input := range inputs {
		key := strings.ToLower(strings.TrimSpace(input.Key))
		if key == "" {
			return "", nil, validationCaseError("context value key is required")
		}
		if _, duplicate := byKey[key]; duplicate {
			return "", nil, validationCaseError("duplicate context value")
		}
		byKey[key] = input.Value
	}
	values := make([]CaseContextValueResponse, 0, len(fields))
	links := []string{}
	for _, field := range fields {
		raw, provided := byKey[field.Key]
		if !provided || len(raw) == 0 || string(raw) == "null" {
			values = append(values, CaseContextValueResponse{
				Key:       field.Key,
				Label:     field.Label,
				FieldType: field.FieldType,
				Required:  field.Required,
				Value:     nil,
			})
			delete(byKey, field.Key)
			continue
		}
		var value any
		switch field.FieldType {
		case model.ContextFieldShortText, model.ContextFieldLongText, model.ContextFieldMessageLink:
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return "", nil, validationCaseError("context value has wrong type: " + field.Key)
			}
			text = strings.TrimSpace(text)
			limit := 4000
			if field.FieldType == model.ContextFieldShortText {
				limit = 500
			}
			if len([]rune(text)) > limit {
				return "", nil, validationCaseError("context value is too long: " + field.Key)
			}
			value = text
			if field.FieldType == model.ContextFieldMessageLink && text != "" {
				links = append(links, text)
			}
		case model.ContextFieldBoolean:
			var boolean bool
			if json.Unmarshal(raw, &boolean) != nil {
				return "", nil, validationCaseError("context value has wrong type: " + field.Key)
			}
			value = boolean
		case model.ContextFieldNumber:
			var number json.Number
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if decoder.Decode(&number) != nil {
				return "", nil, validationCaseError("context value has wrong type: " + field.Key)
			}
			if _, err := number.Float64(); err != nil {
				return "", nil, validationCaseError("context number is invalid: " + field.Key)
			}
			value = number
		default:
			return "", nil, validationCaseError("context field type is invalid")
		}
		values = append(values, CaseContextValueResponse{
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Required:  field.Required,
			Value:     value,
		})
		delete(byKey, field.Key)
	}
	if len(byKey) > 0 {
		return "", nil, validationCaseError("unknown context value")
	}
	body, err := json.Marshal(values)
	if err != nil {
		return "", nil, err
	}
	return string(body), links, nil
}

// parseCaseContextValues decodes the current staff-only context values,
// returning an empty slice (never nil) for malformed JSON.
func parseCaseContextValues(body string) []CaseContextValueResponse {
	var values []CaseContextValueResponse
	if json.Unmarshal([]byte(body), &values) != nil {
		return []CaseContextValueResponse{}
	}
	return values
}

// contextURLPattern finds pasted URLs in prose, Markdown links and angle brackets.
// ParseDiscordMessageLink remains the authority for origin and message identity.
var contextURLPattern = regexp.MustCompile(`https://[^\s<>()]+`)

// contextMessageLinks deduplicates URL aliases by Discord message identity, not
// spelling, so query strings and alternate Discord hosts cannot repeat uploads.
func contextMessageLinks(text string) []DiscordMessageReference {
	var links []DiscordMessageReference
	seen := map[string]bool{}
	for _, raw := range contextURLPattern.FindAllString(text, -1) {
		ref, err := ParseDiscordMessageLink(strings.TrimRight(raw, ".,;!?\"'"))
		if err != nil {
			continue
		}
		key := ref.GuildID + "/" + ref.ChannelID + "/" + ref.MessageID
		if seen[key] {
			continue
		}
		seen[key] = true
		links = append(links, ref)
	}
	return links
}

// ContextContainsMessageLinks reports whether a freeform edit requests optional
// message preservation, so callers keep ordinary context-only feedback unchanged.
func ContextContainsMessageLinks(text string) bool { return len(contextMessageLinks(text)) > 0 }

// captureContextLinks appends only previously unrecorded links. Existing evidence
// is immutable, so removing a link or correcting prose never deletes or recopies
// an archived message. Staff can explicitly retry unavailable captures via Add
// evidence. Authorization and live source-channel checks stay in AddEvidence and
// EvidenceService; no enforcement path is called here.
func (s *CaseService) captureContextLinks(
	ctx context.Context,
	guild *GuildStaffContext,
	detail *CaseDetailResponse,
	text string,
) *CaseDetailResponse {
	links := contextMessageLinks(text)
	if len(links) == 0 {
		return detail
	}
	seen := map[string]bool{}
	for _, evidence := range detail.Evidence {
		ref, err := ParseDiscordMessageLink(evidence.MessageURL)
		if err == nil {
			seen[ref.GuildID+"/"+ref.ChannelID+"/"+ref.MessageID] = true
		}
	}
	warning := false
	attempts := 0
	for _, ref := range links {
		key := ref.GuildID + "/" + ref.ChannelID + "/" + ref.MessageID
		if seen[key] {
			continue
		}
		if attempts >= maxEvidenceMessages || ref.GuildID != guild.Guild.DiscordGuildID {
			warning = true
			continue
		}
		attempts++
		updated, err := s.AddEvidence(ctx, guild, detail.ID, []string{ref.URL}, nil)
		if err != nil {
			warning = true
			continue
		}
		detail = updated
		seen[key] = true
	}
	// Validation or transport failure can occur before an evidence row exists.
	// Preserve that attempt's warning in this response while leaving text saved.
	detail.EvidenceIncomplete = detail.EvidenceIncomplete || warning
	return detail
}

// UpdateContext replaces staff context without changing the snapshotted rule,
// escalation count, validity, or enforcement. An empty value clears context.
// Valid pasted message links are preserved after text commits; optional capture
// failures return the saved detail with EvidenceIncomplete instead of losing text.
func (s *CaseService) UpdateContext(ctx context.Context, guild *GuildStaffContext, caseRef, text string) (*CaseDetailResponse, error) {
	if guild == nil || guild.Guild == nil || !guild.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 4000 {
		return nil, validationCaseError("context must be 4,000 characters or fewer")
	}
	values := []CaseContextValueResponse{}
	if text != "" {
		values = append(values, CaseContextValueResponse{
			Key:       "context",
			Label:     "Context",
			FieldType: model.ContextFieldLongText,
			Value:     text,
		})
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	audit := s.auditEntry(ctx, guild, string(model.AuditActionCaseUpdate), "case", "", model.AuditResultSuccess, "")
	item, err := s.store.UpdateCaseContext(ctx, guild.Guild.ID, strings.TrimSpace(caseRef), string(body), audit)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	detail, err := s.Get(ctx, guild, item.ID)
	if err != nil {
		return nil, err
	}
	return s.captureContextLinks(ctx, guild, detail, text), nil
}
