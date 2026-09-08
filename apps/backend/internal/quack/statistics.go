package quack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

var (
	ErrStatisticsValidation       = errors.New("statistics validation failed")
	ErrStatisticsPermissionDenied = errors.New("statistics permission denied")
)

// StatisticsInput defines an inclusive start and exclusive end for derived staff statistics.
type StatisticsInput struct {
	From string
	To   string
}

// StatisticsRepository derives operational statistics from immutable history.
type StatisticsRepository interface {
	DeriveStaffStatistics(context.Context, model.StaffStatisticsParams) (*model.StaffStatistics, error)
}

// StaffStatisticsService derives guild-scoped operational counts without persisting aggregates or rankings.
type StaffStatisticsService struct {
	store StatisticsRepository
}

// NewStaffStatisticsService constructs the derived statistics capability over the existing source-of-truth repository.
func NewStaffStatisticsService(store StatisticsRepository) *StaffStatisticsService {
	return &StaffStatisticsService{store: store}
}

// Get returns a guild-scoped derived snapshot for an authorized moderator.
func (s *StaffStatisticsService) Get(ctx context.Context, guildContext *GuildStaffContext, input StatisticsInput) (*model.StaffStatistics, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("statistics service is not configured")
	}
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(model.PermissionActionAuditRead) {
		return nil, ErrStatisticsPermissionDenied
	}
	from, to, err := statisticsRange(input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return s.store.DeriveStaffStatistics(ctx, model.StaffStatisticsParams{GuildID: guildContext.Guild.ID, From: from, To: to})
}

// statisticsRange normalizes the requested window to UTC and bounds expensive
// history queries to one year. Omitted bounds select the preceding month.
func statisticsRange(input StatisticsInput, now time.Time) (time.Time, time.Time, error) {
	to := now.UTC()
	var from time.Time
	var err error
	if strings.TrimSpace(input.To) != "" {
		to, err = time.Parse(time.RFC3339, strings.TrimSpace(input.To))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: to must use RFC3339", ErrStatisticsValidation)
		}
	}
	from = to.AddDate(0, -1, 0)
	if strings.TrimSpace(input.From) != "" {
		from, err = time.Parse(time.RFC3339, strings.TrimSpace(input.From))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: from must use RFC3339", ErrStatisticsValidation)
		}
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: range must be positive and at most 366 days", ErrStatisticsValidation)
	}
	return from, to, nil
}
