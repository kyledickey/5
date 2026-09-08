package tickets

import "encoding/json"

// ValidateEnabledConfiguration applies the module's existing enabled-state
// invariants without writing configuration or changing runtime behavior.
func ValidateEnabledConfiguration(raw string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	return validateSettings(settings, true)
}
