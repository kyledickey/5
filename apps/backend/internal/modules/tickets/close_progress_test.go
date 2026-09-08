package tickets_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// progressDiscordFake records external ordering independently of persisted state.
type progressDiscordFake struct {
	*discordFake
	order []string
}

// PublishTicketQueue records the transcript publication boundary only.
func (f *progressDiscordFake) PublishTicketQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if transcript != nil {
		f.order = append(f.order, "publish")
	}
	return f.discordFake.PublishTicketQueue(ctx, ticket, settings, transcript)
}

// DeleteTicketChannel records attempts, including recoverable external failures.
func (f *progressDiscordFake) DeleteTicketChannel(ctx context.Context, thread string) error {
	f.order = append(f.order, "delete")
	return f.discordFake.DeleteTicketChannel(ctx, thread)
}

// TestCloseProgressFollowsPublication verifies truthful acknowledgement ordering
// and that publication, acknowledgement, and deletion failures remain failures.
func TestCloseProgressFollowsPublication(t *testing.T) {
	for _, scenario := range []string{"success", "publish_failure", "ack_failure", "delete_failure"} {
		t.Run(scenario, func(t *testing.T) {
			_, service, _ := journalSetup(t)
			client := &progressDiscordFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := adapter.Open(context.Background(), actor)
			if err != nil {
				t.Fatal(err)
			}
			client.failPublish = scenario == "publish_failure"
			if scenario == "delete_failure" {
				client.failArchive = 1
			}
			_, err = adapter.CloseWithProgress(context.Background(), actor, ticket.ID, func(saved *tickets.Ticket) error {
				client.order = append(client.order, "ack")
				detail, _, detailErr := service.Detail(context.Background(), actor, ticket.ID)
				if detailErr != nil || saved.TranscriptURL == "" || detail.TranscriptURL == "" {
					t.Fatal("ack before durable receipt", detailErr)
				}
				if scenario == "ack_failure" {
					return errors.New("response unavailable")
				}
				return nil
			})
			want := []string{"publish", "ack", "delete"}
			if scenario == "publish_failure" {
				want = []string{"publish"}
			}
			if scenario == "ack_failure" {
				want = []string{"publish", "ack"}
			}
			if !reflect.DeepEqual(client.order, want) {
				t.Fatal(client.order, want)
			}
			if (err == nil) != (scenario == "success") {
				t.Fatal("incorrect terminal result", scenario, err)
			}
		})
	}
}
