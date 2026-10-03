package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

// TestGatewayTicketReads preserves guild isolation, all-status deletion lookup,
// and bounded open-only repair traversal independently of ticket enablement.
func TestGatewayTicketReads(t *testing.T) {
	db, service, _ := journalSetup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 102; i >= 0; i-- {
		if err := db.Table("tickets").Create(map[string]any{
			"id": fmt.Sprintf("ticket-%03d", i), "guild_id": "repair-guild",
			"thread_discord_channel_id": fmt.Sprintf("thread-%03d", i),
			"owner_discord_user_id":     fmt.Sprintf("owner-%03d", i),
			"status":                    tickets.StatusOpen, "metadata_json": "{}",
			"created_at": now, "updated_at": now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Interleave excluded records so a broad query changes both pages' contents.
	for _, record := range []map[string]any{
		{"id": "ticket-050-other", "guild_id": "other-guild", "thread_discord_channel_id": "other-thread", "owner_discord_user_id": "other", "status": tickets.StatusOpen, "metadata_json": "{}", "created_at": now, "updated_at": now},
		{"id": "ticket-050-resolved", "guild_id": "repair-guild", "thread_discord_channel_id": "resolved-thread", "owner_discord_user_id": "resolved", "status": tickets.StatusResolved, "metadata_json": "{}", "created_at": now, "updated_at": now},
	} {
		if err := db.Table("tickets").Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		guild, channel, want string
	}{
		{"repair-guild", "thread-000", "ticket-000"},
		{"repair-guild", "resolved-thread", "ticket-050-resolved"},
		{"other-guild", "thread-000", ""},
		{"repair-guild", "unknown", ""},
	} {
		id, err := service.DeletedChannelTicketID(ctx, test.guild, test.channel)
		if test.want == "" {
			if !errors.Is(err, tickets.ErrNotFound) || id != "" {
				t.Fatalf("missing lookup = %q, %v", id, err)
			}
		} else if err != nil || id != test.want {
			t.Fatalf("lookup = %q, %v; want %q", id, err, test.want)
		}
	}
	first, err := service.OpenThreadRepairPage(ctx, "repair-guild", "")
	if err != nil || len(first) != 100 {
		t.Fatalf("first page length = %d, err = %v", len(first), err)
	}
	second, err := service.OpenThreadRepairPage(ctx, "repair-guild", first[len(first)-1].ID)
	if err != nil || len(second) != 3 {
		t.Fatalf("second page length = %d, err = %v", len(second), err)
	}
	for i, target := range append(first, second...) {
		if target.ID != fmt.Sprintf("ticket-%03d", i) || target.ThreadDiscordChannelID != fmt.Sprintf("thread-%03d", i) || target.OwnerDiscordUserID != fmt.Sprintf("owner-%03d", i) {
			t.Fatalf("repair target %d = %+v", i, target)
		}
	}
	last, err := service.OpenThreadRepairPage(ctx, "repair-guild", second[len(second)-1].ID)
	if err != nil || len(last) != 0 {
		t.Fatalf("terminal page = %+v, %v", last, err)
	}
}

// TestGatewayTicketReadFailures ensures a failed lookup cannot be mistaken for
// a missing ticket or an exhausted repair page by gateway callers.
func TestGatewayTicketReadFailures(t *testing.T) {
	db, service, _ := journalSetup(t)
	want := errors.New("ticket lookup failed")
	if err := db.Callback().Query().Before("gorm:query").Register("fail_gateway_reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "tickets" {
			tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DeletedChannelTicketID(context.Background(), "guild", "thread"); !errors.Is(err, want) {
		t.Fatal("deleted channel lookup error", err)
	}
	if _, err := service.OpenThreadRepairPage(context.Background(), "guild", ""); !errors.Is(err, want) {
		t.Fatal("repair page error", err)
	}
}

// TestTicketStateOperationsDoNotLoadTimeline makes discarded history queries fail
// while exercising both transcript ports and the existing access/cleanup lifecycle.
func TestTicketStateOperationsDoNotLoadTimeline(t *testing.T) {
	for _, native := range []bool{false, true} {
		name := "legacy"
		if native {
			name = "native"
		}
		t.Run(name, func(t *testing.T) {
			db, service, _ := journalSetup(t)
			ctx := context.Background()
			client := &discordFake{}
			var transport tickets.DiscordClient = client
			if native {
				transport = &journalDiscordFake{discordFake: client}
			}
			adapter := tickets.NewDiscordAdapter(service, transport)
			owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}
			ticket, err := adapter.Open(ctx, owner)
			if err != nil {
				t.Fatal(err)
			}
			historyError := errors.New("unexpected timeline query")
			historyReads := 0
			if err := db.Callback().Query().Before("gorm:query").Register("reject_unused_timeline", func(tx *gorm.DB) {
				if tx.Statement.Table == "ticket_events" {
					historyReads++
					tx.AddError(historyError)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Reply(ctx, owner, ticket.ID, "retained reply"); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Join(ctx, staff, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if err := adapter.RepairPermissions(ctx, staff, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
				t.Fatal(err)
			}
			if pending, err := service.ClosurePending(ctx, staff, ticket.ID); err != nil || pending {
				t.Fatal("closure state", pending, err)
			}
			if historyReads != 0 {
				t.Fatal("state operation queried timeline", historyReads)
			}
			// Detail still intentionally loads history and preserves its query failure.
			if _, _, err := service.Detail(ctx, owner, ticket.ID); !errors.Is(err, historyError) {
				t.Fatal("detail hid timeline failure", err)
			}
			if historyReads != 1 {
				t.Fatal("detail did not read history")
			}
			if err := db.Callback().Query().Remove("reject_unused_timeline"); err != nil {
				t.Fatal(err)
			}
			_, events, err := service.Detail(ctx, owner, ticket.ID)
			if err != nil || len(events) < 3 {
				t.Fatal("detail lost events", events, err)
			}
			for i := 1; i < len(events); i++ {
				if events[i].CreatedAt.Before(events[i-1].CreatedAt) {
					t.Fatal("timeline order changed")
				}
			}
		})
	}
}

// TestTicketStateReadsRecheckAuthority verifies no authorization result is cached
// and wrong-guild/unknown-record errors are retained before any history lookup.
func TestTicketStateReadsRecheckAuthority(t *testing.T) {
	db, service, _ := journalSetup(t)
	ctx := context.Background()
	ticket, err := service.Open(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, "thread")
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	if err := db.Callback().Query().Before("gorm:query").Register("count_state_history", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_events" {
			reads++
		}
	}); err != nil {
		t.Fatal(err)
	}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	if _, err := service.ClosurePending(ctx, staff, ticket.ID); err != nil {
		t.Fatal(err)
	}
	staff.CanModerate = false
	if _, err := service.ClosurePending(ctx, staff, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatal("revoked staff retained access", err)
	}
	for _, scenario := range []struct {
		actor tickets.Actor
		id    string
		want  error
	}{
		{tickets.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID, tickets.ErrPermissionDenied},
		{tickets.Actor{GuildID: "other-guild", DiscordUserID: "owner", CanModerate: true}, ticket.ID, tickets.ErrNotFound},
		{tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, "missing", tickets.ErrNotFound},
	} {
		if _, err := service.ClosurePending(ctx, scenario.actor, scenario.id); !errors.Is(err, scenario.want) {
			t.Fatal(err, scenario.want)
		}
		if _, _, err := service.Detail(ctx, scenario.actor, scenario.id); !errors.Is(err, scenario.want) {
			t.Fatal(err, scenario.want)
		}
	}
	if reads != 0 {
		t.Fatal("authorization failure loaded private history")
	}
	lookupError := errors.New("ticket storage unavailable")
	if err := db.Callback().Query().Before("gorm:query").Register("fail_ticket_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "tickets" {
			tx.AddError(lookupError)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClosurePending(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}, ticket.ID); !errors.Is(err, lookupError) {
		t.Fatal("state lookup failure hidden", err)
	}
}

func TestRouteRegistrarStatusAndAuthorization(t *testing.T) {
	_, service, _ := setup(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	tickets.RegisterRoutes(engine.Group("/guilds/:guildID/modules"), service, func(c *gin.Context) (tickets.Actor, error) {
		return tickets.Actor{GuildID: c.Param("guildID"), DiscordUserID: "staff", CanModerate: true}, nil
	}, nil)
	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-a/modules/tickets/status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/guilds/guild-a/modules/tickets/ticket/close", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing closer did not fail safely: %d", response.Code)
	}

}

func TestTicketReopenRouteIsRemoved(t *testing.T) {
	_, service, _ := setup(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	tickets.RegisterRoutes(engine.Group("/guilds/:guildID/modules"), service, func(c *gin.Context) (tickets.Actor, error) {
		return tickets.Actor{GuildID: c.Param("guildID"), DiscordUserID: "staff", CanModerate: true}, nil
	}, nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/guilds/guild-a/modules/tickets/ticket/reopen", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("reopen route remains: %d", response.Code)
	}
}

// TestEveryTicketCloseRoutePreservesDiscordTranscript verifies aliases cannot
// replace the actual conversation with caller-provided text or skip publication.
func TestEveryTicketCloseRoutePreservesDiscordTranscript(t *testing.T) {
	for _, action := range []string{"close", "resolve", "cancel"} {
		t.Run(action, func(t *testing.T) {
			_, service, _ := setup(t)
			client := &discordFake{}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := adapter.Open(context.Background(), actor)
			if err != nil {
				t.Fatal(err)
			}
			engine := gin.New()
			tickets.RegisterRoutes(engine.Group("/guilds/:guildID/modules"), service, func(*gin.Context) (tickets.Actor, error) { return actor, nil }, adapter)
			request := func() *httptest.ResponseRecorder {
				response := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/guilds/guild-a/modules/tickets/"+ticket.ID+"/"+action, strings.NewReader(`{"transcript":"fabricated"}`))
				req.Header.Set("Content-Type", "application/json")
				engine.ServeHTTP(response, req)
				return response
			}
			client.failPublish = true
			if response := request(); response.Code == http.StatusOK || client.archiveAttempts != 0 {
				t.Fatalf("failed publication deleted thread: %d %+v", response.Code, client)
			}
			transcript, err := service.Transcript(context.Background(), actor, ticket.ID)
			if err != nil || transcript.Content != "captured" {
				t.Fatalf("real transcript not retained: %+v %v", transcript, err)
			}
			client.failPublish = false
			if response := request(); response.Code != http.StatusOK || client.archiveAttempts != 1 {
				t.Fatalf("close retry failed: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
