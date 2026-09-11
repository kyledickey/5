package ui

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestSetupChannelFailuresKeepTheirUserCopy proves the sentences administrators
// see from setup are byte-for-byte unchanged whether callers read them through
// UserMessage or the older err.Error() path, including when the error is wrapped.
func TestSetupChannelFailuresKeepTheirUserCopy(t *testing.T) {
	_, err := SetupChannel(context.Background(), nil, "guild", "", "", "appeals", SetupStaffChannel)
	const want = "Quack is not connected. Try again shortly."
	if err == nil || err.Error() != want {
		t.Fatalf("err.Error() changed the copy: %v", err)
	}
	wrapped := fmt.Errorf("setup: %w", err)
	if message, ok := UserMessage(wrapped); !ok || message != want {
		t.Fatalf("UserMessage lost the copy through wrapping: %q %v", message, ok)
	}
	if _, ok := UserMessage(errors.New("internal failure")); ok {
		t.Fatal("internal errors must not be shown to users")
	}
	create := &UserError{Message: fmt.Sprintf(
		"Could not create #%s. Quack needs Manage Channels permission. You can also specify an existing channel.",
		"appeals",
	)}
	if create.Error() != "Could not create #appeals. Quack needs Manage Channels permission. You can also specify an existing channel." {
		t.Fatalf("creation copy changed: %s", create.Error())
	}
}
