package routes

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/quack"
)

// schemaReadinessProvider reports schema presence without coupling HTTP health
// checks to the SQL adapter.
type schemaReadinessProvider interface {
	SchemaReady(context.Context) error
}

// operationalMetricProvider exposes aggregate low-cardinality counters.
type operationalMetricProvider interface {
	OperationalMetricSnapshot(context.Context) (map[string]int64, error)
}

// readinessCheck is one dependency or runtime capability in the readiness contract.
type readinessCheck struct {
	Ready   bool   `json:"ready"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

// readinessResponse is the fail-closed aggregate returned by /readyz.
type readinessResponse struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks"`
}

// liveness reports only whether the process can serve requests. Dependency
// outages must not cause an orchestrator restart loop.
// @Summary Report process liveness
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /livez [get]
func liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"live": true})
}

// readiness reports every dependency required to accept moderation work.
// @Summary Report dependency readiness
// @Tags Health
// @Produce json
// @Success 200 {object} readinessResponse
// @Failure 503 {object} readinessResponse
// @Router /readyz [get]
func readiness(c *gin.Context, services *quack.Services, discord DiscordStatusProvider) {
	result := readinessResponse{Ready: true, Checks: map[string]readinessCheck{}}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if services == nil || services.Store == nil {
		result.Checks["database"] = readinessCheck{Ready: false, Detail: "dependency unavailable"}
		result.Checks["redis"] = readinessCheck{Ready: false, Detail: "dependency unavailable"}
	} else {
		result.Checks["database"] = timedReadiness(func() error { return services.Store.PingDatabase(ctx) })
		result.Checks["redis"] = timedReadiness(func() error { return services.Store.PingRedis(ctx) })
	}
	connected, _, latency := false, "", int64(0)
	if discord != nil {
		connected, _, latency = discord.Status()
	}
	result.Checks["discord"] = readinessCheck{Ready: connected, Latency: latency, Detail: readinessDetail(connected, "gateway disconnected")}

	queueReady := false
	var opsStatus *quack.OpsStatusResponse
	if services != nil && services.Ops != nil {
		if status, err := services.Ops.GlobalStatus(ctx); err == nil {
			opsStatus = status
			queueReady = status.Queue.Active
		}
	}
	result.Checks["queue"] = readinessCheck{Ready: queueReady, Detail: readinessDetail(queueReady, "action queue inactive")}

	// The "migration" check name is part of the published /readyz contract.
	schema := readinessCheck{Ready: false, Detail: "schema status unavailable"}
	if services != nil {
		if provider, ok := services.Store.(schemaReadinessProvider); ok {
			if err := provider.SchemaReady(ctx); err == nil {
				schema = readinessCheck{Ready: true}
			} else {
				schema.Detail = "schema is not initialized"
			}
		}
	}
	result.Checks["migration"] = schema

	actionsReady := opsStatus != nil
	if opsStatus != nil {
		for _, capability := range opsStatus.Actions.Capabilities {
			if !capability.Executable {
				actionsReady = false
				break
			}
		}
	}

	result.Checks["action_capabilities"] = readinessCheck{Ready: actionsReady, Detail: readinessDetail(actionsReady, "required action unavailable")}

	for _, check := range result.Checks {
		if !check.Ready {
			result.Ready = false
			break
		}
	}
	status := http.StatusOK
	if !result.Ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, result)
}

// timedReadiness runs one already-bounded dependency check and reports only a
// safe classification rather than raw adapter errors.
func timedReadiness(check func() error) readinessCheck {
	started := time.Now()
	err := check()
	ready := err == nil
	return readinessCheck{Ready: ready, Latency: time.Since(started).Milliseconds(), Detail: readinessDetail(ready, "dependency unavailable")}
}

// readinessDetail avoids repeating success text while preserving an actionable class.
func readinessDetail(ready bool, unavailable string) string {
	if ready {
		return ""
	}
	return unavailable
}

// metrics emits a token-protected Prometheus text snapshot with no actor,
// guild, member, content, URL, or payload labels.
// @Summary Export operational metrics
// @Tags Operations
// @Produce text/plain
// @Security MetricsKey
// @Success 200 {string} string
// @Failure 403 {string} string
// @Failure 404 {string} string
// @Failure 503 {string} string
// @Router /metrics [get]
func metrics(c *gin.Context, services *quack.Services) {
	configured := strings.TrimSpace(services.Config.Observability.MetricsToken)
	provided := strings.TrimSpace(c.GetHeader("X-Quack-Metrics-Key"))
	if configured == "" {
		c.Status(http.StatusNotFound)
		return
	}
	if len(configured) != len(provided) || subtle.ConstantTimeCompare([]byte(configured), []byte(provided)) != 1 {
		c.Status(http.StatusForbidden)
		return
	}
	provider, ok := services.Store.(operationalMetricProvider)
	if !ok {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	snapshot, err := provider.OperationalMetricSnapshot(c.Request.Context())
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if status, statusErr := services.Ops.GlobalStatus(c.Request.Context()); statusErr == nil {
		snapshot["quack_action_queue_depth"] = int64(status.Queue.QueueSize)
		snapshot["quack_action_queue_failures_total"] = int64(status.Queue.FailedTotal)
		snapshot["quack_action_retrying_current"] = status.Actions.StatusCounts["retrying"]
	}
	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var output strings.Builder
	for _, key := range keys {
		_, _ = fmt.Fprintf(&output, "%s %d\n", key, snapshot[key])
	}
	c.String(http.StatusOK, output.String())
}

// DiscordStatusProvider supplies gateway status without coupling the health
// routes to the bot adapter.
type DiscordStatusProvider interface {
	Status() (connected bool, username string, latencyMS int64)
}

type discordStatus struct {
	Connected bool   `json:"connected"`
	Username  string `json:"username,omitempty"`
	Latency   int64  `json:"latency,omitempty"`
}

type dependencyStatus struct {
	Connected bool  `json:"connected"`
	Latency   int64 `json:"latency,omitempty"`
}

// status reports adapter connectivity without a readiness verdict.
// @Summary Report service status
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /status [get]
func status(c *gin.Context, services *quack.Services, discord DiscordStatusProvider) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	connected, username, latency := false, "", int64(0)
	if discord != nil {
		connected, username, latency = discord.Status()
	}
	var redis, database dependencyStatus
	if services != nil && services.Store != nil {
		redis = pingStatus(ctx, services.Store.PingRedis)
		database = pingStatus(ctx, services.Store.PingDatabase)
	}
	c.JSON(http.StatusOK, gin.H{
		"discord":  discordStatus{Connected: connected, Username: username, Latency: latency},
		"redis":    redis,
		"database": database,
	})
}

// pingStatus times one dependency probe and reports only connectivity, never
// the adapter error.
func pingStatus(ctx context.Context, ping func(context.Context) error) dependencyStatus {
	start := time.Now()
	if err := ping(ctx); err != nil {
		return dependencyStatus{Connected: false}
	}
	return dependencyStatus{Connected: true, Latency: time.Since(start).Milliseconds()}
}
