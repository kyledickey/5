package interactions

import (
	"errors"
	"strings"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// ErrComponentHandlerNotFound is reserved for lookups against a missing registry;
// an unknown custom ID is reported through the boolean result instead.
var ErrComponentHandlerNotFound = errors.New("component handler not found")

// ComponentRegistry maps "namespace:action" keys to button/select handlers and,
// separately, to modal-submit handlers. Registration happens during startup
// before the gateway opens; lookups afterwards are read-only, so no lock is needed.
type ComponentRegistry struct {
	components map[string]ui.Handler
	modals     map[string]ui.Handler
}

// NewComponentRegistry returns an empty registry.
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: map[string]ui.Handler{},
		modals:     map[string]ui.Handler{},
	}
}

// RegisterComponent binds a button or select handler to namespace:action.
// Registering the same key twice is an error so two features cannot silently
// compete for one custom ID.
func (r *ComponentRegistry) RegisterComponent(namespace, action string, handler ui.Handler) error {
	return r.register(r.components, namespace, action, handler)
}

// RegisterModal binds a modal-submit handler to namespace:action, in a keyspace
// separate from components so a form and its opening button may share a name.
func (r *ComponentRegistry) RegisterModal(namespace, action string, handler ui.Handler) error {
	return r.register(r.modals, namespace, action, handler)
}

// LookupComponent decodes a component custom ID and returns its handler. The
// boolean is false for an unregistered key; the error reports a malformed ID.
func (r *ComponentRegistry) LookupComponent(customID string) (ui.Handler, bool, error) {
	return r.lookup(r.components, customID)
}

// LookupModal is LookupComponent for modal submissions.
func (r *ComponentRegistry) LookupModal(customID string) (ui.Handler, bool, error) {
	return r.lookup(r.modals, customID)
}

func (r *ComponentRegistry) register(target map[string]ui.Handler, namespace, action string, handler ui.Handler) error {
	if handler == nil {
		return errors.New("component handler is required")
	}
	key := Key(namespace, action)
	if key == ":" || strings.HasPrefix(key, ":") || strings.HasSuffix(key, ":") {
		return errors.New("component namespace and action are required")
	}
	if _, exists := target[key]; exists {
		return errors.New("component handler is already registered")
	}
	target[key] = handler
	return nil
}

func (r *ComponentRegistry) lookup(source map[string]ui.Handler, customID string) (ui.Handler, bool, error) {
	parsed, err := ui.DecodeCustomID(customID)
	if err != nil {
		return nil, false, err
	}
	handler, ok := source[Key(parsed.Namespace, parsed.Action)]
	return handler, ok, nil
}
