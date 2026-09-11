package quack

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// OpsService assembles queue health and action capability data for operational endpoints.
type OpsService struct {
	store     OpsRepository
	scheduler CaseWorkScheduler
}

// OpsStatusResponse is the operator status document: transient queue counters
// from the scheduler plus durable action counts from the repository, either
// process-wide (Scope "global") or restricted to one guild (Scope "guild").
type OpsStatusResponse struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Scope       string          `json:"scope"`
	GuildID     string          `json:"guild_id,omitempty"`
	Queue       QueueStats      `json:"queue"`
	Actions     OpsActionStatus `json:"actions"`
}

// OpsActionStatus summarizes persisted enforcement work: which action types
// this build can execute, execution counts by status, the oldest row still
// waiting, and the most recent failures.
type OpsActionStatus struct {
	Capabilities         []OpsActionCapability     `json:"capabilities"`
	StatusCounts         map[string]int64          `json:"status_counts"`
	OldestPendingOrRetry *OpsOldestActionExecution `json:"oldest_pending_or_retry,omitempty"`
	RecentFailures       []OpsRecentActionFailure  `json:"recent_failures"`
}

// OpsActionCapability reports whether one action type is executable by this
// build and how ("implemented" directly or as a "staff_confirmed_reversal").
type OpsActionCapability struct {
	ActionType model.ActionType `json:"action_type"`
	Executable bool             `json:"executable"`
	Status     string           `json:"status"`
}

// OpsOldestActionExecution identifies the longest-waiting pending or retrying
// execution so operators can tell a stalled queue from an empty one.
type OpsOldestActionExecution struct {
	ID          string                      `json:"id"`
	CaseID      string                      `json:"case_id"`
	CaseNumber  uint64                      `json:"case_number"`
	ActionType  model.ActionType            `json:"action_type"`
	Status      model.ActionExecutionStatus `json:"status"`
	CreatedAt   time.Time                   `json:"created_at"`
	NextRetryAt *time.Time                  `json:"next_retry_at,omitempty"`
}

// OpsRecentActionFailure is one recently failed execution with its classified error.
type OpsRecentActionFailure struct {
	ID            string                      `json:"id"`
	CaseID        string                      `json:"case_id"`
	CaseNumber    uint64                      `json:"case_number"`
	ActionType    model.ActionType            `json:"action_type"`
	Status        model.ActionExecutionStatus `json:"status"`
	LastErrorCode string                      `json:"last_error_code,omitempty"`
	LastError     string                      `json:"last_error,omitempty"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

// NewOpsService returns a service over store. scheduler may be nil, in which
// case responses carry zero queue statistics.
func NewOpsService(store OpsRepository, scheduler CaseWorkScheduler) *OpsService {
	return &OpsService{store: store, scheduler: scheduler}
}

// GlobalStatus returns process-wide queue and action health for privileged operators.
func (s *OpsService) GlobalStatus(ctx context.Context) (*OpsStatusResponse, error) {
	return s.status(ctx, "", "global")
}

// GuildStatus returns the same operational view restricted to one guild's persisted actions.
func (s *OpsService) GuildStatus(ctx context.Context, guildID string) (*OpsStatusResponse, error) {
	if guildID == "" {
		return nil, errors.New("guild id is required")
	}
	return s.status(ctx, guildID, "guild")
}

// status combines durable action state with transient worker statistics so operators can distinguish backlog from queue health.
func (s *OpsService) status(ctx context.Context, guildID, scope string) (*OpsStatusResponse, error) {
	snapshot, err := s.store.ActionQueueSnapshot(ctx, guildID, 10)
	if err != nil {
		return nil, err
	}

	queueStats := QueueStats{}
	if s.scheduler != nil {
		queueStats = s.scheduler.Stats()
	}

	return &OpsStatusResponse{
		GeneratedAt: time.Now().UTC(),
		Scope:       scope,
		GuildID:     guildID,
		Queue:       queueStats,
		Actions:     opsActionStatus(snapshot),
	}, nil
}

// opsActionStatus maps the repository snapshot into the stable operations response and always includes current action capabilities.
func opsActionStatus(snapshot *model.ActionQueueSnapshot) OpsActionStatus {
	status := OpsActionStatus{
		Capabilities: actionCapabilities(),
		StatusCounts: map[string]int64{},
	}
	if snapshot == nil {
		return status
	}
	for _, row := range snapshot.StatusCounts {
		status.StatusCounts[string(row.Status)] = row.Count
	}
	if snapshot.OldestPendingOrRetry != nil {
		oldest := snapshot.OldestPendingOrRetry
		status.OldestPendingOrRetry = &OpsOldestActionExecution{
			ID:          oldest.ID,
			CaseID:      oldest.CaseID,
			CaseNumber:  oldest.CaseNumber,
			ActionType:  oldest.ActionType,
			Status:      oldest.Status,
			CreatedAt:   oldest.CreatedAt,
			NextRetryAt: oldest.NextRetryAt,
		}
	}
	status.RecentFailures = make([]OpsRecentActionFailure, 0, len(snapshot.RecentFailures))
	for _, failure := range snapshot.RecentFailures {
		status.RecentFailures = append(status.RecentFailures, OpsRecentActionFailure{
			ID:            failure.ID,
			CaseID:        failure.CaseID,
			CaseNumber:    failure.CaseNumber,
			ActionType:    failure.ActionType,
			Status:        failure.Status,
			LastErrorCode: failure.LastErrorCode,
			LastError:     failure.LastError,
			UpdatedAt:     failure.UpdatedAt,
		})
	}
	return status
}

// actionCapabilities lists the enforcement actions this build implements.
// Reversals are executable but only ever queued after staff confirmation.
func actionCapabilities() []OpsActionCapability {
	return []OpsActionCapability{
		{ActionType: model.ActionTimeoutUser, Executable: true, Status: "implemented"},
		{ActionType: model.ActionKickUser, Executable: true, Status: "implemented"},
		{ActionType: model.ActionBanUser, Executable: true, Status: "implemented"},
		{ActionType: model.ActionRemoveTimeout, Executable: true, Status: "staff_confirmed_reversal"},
		{ActionType: model.ActionUnbanUser, Executable: true, Status: "staff_confirmed_reversal"},
	}
}
