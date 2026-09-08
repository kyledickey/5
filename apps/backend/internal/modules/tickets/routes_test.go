package tickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

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
