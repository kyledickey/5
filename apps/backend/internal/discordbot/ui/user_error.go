package ui

import "errors"

// UserError carries a sentence that is meant to be shown to the person who
// invoked a command, unchanged. It exists so Discord-facing copy ("Could not
// create #appeals. Quack needs Manage Channels permission.") can travel through
// an error return without being reduced to a lowercase log string.
//
// Error returns Message verbatim: callers in other packages already surface
// SetupChannel failures with err.Error(), and the copy they show must not change.
// Callers that want to distinguish user copy from internal failures use UserMessage.
type UserError struct {
	Message string
}

// Error returns the user-facing sentence exactly as written.
func (e *UserError) Error() string {
	return e.Message
}

// UserMessage reports whether err (or any error it wraps) is a UserError and,
// if so, returns the copy that should be shown to the invoking user.
func UserMessage(err error) (string, bool) {
	var user *UserError
	if errors.As(err, &user) {
		return user.Message, true
	}
	return "", false
}
