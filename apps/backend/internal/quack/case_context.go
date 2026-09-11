package quack

import (
	"encoding/json"
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
