package store

import (
	"context"
	"fmt"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// ActionQueueSnapshot summarizes the action queue for operators: counts by
// status, the oldest pending or retrying execution, and the most recent
// failures. An empty guildID spans every guild; recentLimit is clamped to 1..25.
func (s *Store) ActionQueueSnapshot(ctx context.Context, guildID string, recentLimit int) (*model.ActionQueueSnapshot, error) {
	if recentLimit <= 0 {
		recentLimit = 10
	}
	if recentLimit > 25 {
		recentLimit = 25
	}

	base := s.db.WithContext(ctx).
		Table("case_action_executions AS e").
		Joins("JOIN cases AS c ON c.id = e.case_id")
	if guildID != "" {
		base = base.Where("c.guild_id = ?", guildID)
	}

	var statusCounts []model.ActionStatusCount
	if err := base.Session(&gorm.Session{}).Select("e.status AS status, COUNT(*) AS count").Group("e.status").Scan(&statusCounts).Error; err != nil {
		return nil, fmt.Errorf("count action executions by status: %w", err)
	}

	var oldest model.OldestActionExecution
	oldestResult := base.Session(&gorm.Session{}).
		Select("e.id, e.case_id, c.case_number, e.action_type, e.status, e.created_at, e.next_retry_at").
		Where("e.status IN ?", []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRetrying}).
		Order("COALESCE(e.next_retry_at, e.created_at) ASC").
		Limit(1).
		Scan(&oldest)
	if oldestResult.Error != nil {
		return nil, fmt.Errorf("get oldest pending action: %w", oldestResult.Error)
	}

	var failures []model.RecentActionFailure
	if err := base.Session(&gorm.Session{}).
		Select("e.id, e.case_id, c.case_number, e.action_type, e.status, e.last_error_code, e.last_error, e.updated_at").
		Where("e.status = ? OR e.last_error_code <> ''", model.ActionExecutionFailed).
		Order("e.updated_at DESC").
		Limit(recentLimit).
		Scan(&failures).Error; err != nil {
		return nil, fmt.Errorf("list recent action failures: %w", err)
	}

	snapshot := &model.ActionQueueSnapshot{
		StatusCounts:   statusCounts,
		RecentFailures: failures,
	}
	if oldest.ID != "" {
		snapshot.OldestPendingOrRetry = &oldest
	}
	return snapshot, nil
}
