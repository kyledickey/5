package discordbot

import (
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"net/http"
	"testing"
)

// TestAppealSendErrorRetriesOnlyKnownRejections keeps ambiguous transport failures
// out of automatic retry while allowing channel/permission/rate-limit repair.
func TestAppealSendErrorRetriesOnlyKnownRejections(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		err := appealSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, quack.ErrAppealDeliveryDeferred) != (status != 500) {
			t.Fatalf("incorrect retry classification for %d: %v", status, err)
		}
	}
	if err := appealSendError(errors.New("connection reset after writing request")); errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatal("uncertain send was retried")
	}
}
