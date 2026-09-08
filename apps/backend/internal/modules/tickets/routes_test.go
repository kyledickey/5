package tickets_test

import (
	"net/http"
	"net/http/httptest"
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
	})
	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-a/modules/tickets/status", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTicketReopenRouteIsRemoved(t *testing.T) {
	_, service, _ := setup(t)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	tickets.RegisterRoutes(engine.Group("/guilds/:guildID/modules"), service, func(c *gin.Context) (tickets.Actor, error) {
		return tickets.Actor{GuildID: c.Param("guildID"), DiscordUserID: "staff", CanModerate: true}, nil
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/guilds/guild-a/modules/tickets/ticket/reopen", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("reopen route remains: %d", response.Code)
	}
}
