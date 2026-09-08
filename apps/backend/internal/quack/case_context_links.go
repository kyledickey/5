package quack

import (
	"context"
	"regexp"
	"strings"
)

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
func (s *CaseService) captureContextLinks(ctx context.Context, guild *GuildStaffContext, detail *CaseDetailResponse, text string) *CaseDetailResponse {
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
