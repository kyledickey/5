package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// DeriveStaffStatistics aggregates case, action, appeal, and audit counts for
// one guild over [From, To) by UTC day and by each source's dimensions. It
// scans grouped projections only, never full rows, so memory scales with the
// number of distinct buckets rather than records.
func (s *Store) DeriveStaffStatistics(ctx context.Context, params model.StaffStatisticsParams) (*model.StaffStatistics, error) {
	if params.GuildID == "" || params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("invalid staff statistics range")
	}

	daySQL, dayArgs := statisticsDayExpression(s.db, params)
	result := &model.StaffStatistics{From: params.From.UTC(), To: params.To.UTC()}
	groups := []struct {
		table      any
		scope      string
		args       []any
		dimensions []string
		total      *int64
		buckets    []*[]model.StatisticBucket
		name       string
	}{
		{
			table:      &model.Case{},
			scope:      "guild_id = ?",
			args:       []any{params.GuildID},
			dimensions: []string{"CASE WHEN template_id IS NULL OR LENGTH(template_id) = 0 THEN 'historical_or_deleted' ELSE template_id END", "status", "source"},
			total:      &result.CaseTotal,
			buckets:    []*[]model.StatisticBucket{&result.CasesByDay, &result.CasesByTemplate, &result.CasesByValidity, &result.CasesBySource},
			name:       "case",
		},
		{
			table:      &model.CaseActionExecution{},
			scope:      "case_id IN (SELECT id FROM cases WHERE guild_id = ?)",
			args:       []any{params.GuildID},
			dimensions: []string{"action_type", "status"},
			total:      &result.ActionTotal,
			buckets:    []*[]model.StatisticBucket{&result.ActionsByDay, &result.ActionsByType, &result.ActionsByResult},
			name:       "action",
		},
		{
			table:      &model.Appeal{},
			scope:      "guild_id = ?",
			args:       []any{params.GuildID},
			dimensions: []string{"status"},
			total:      &result.AppealTotal,
			buckets:    []*[]model.StatisticBucket{&result.AppealsByDay, &result.AppealsByStatus},
			name:       "appeal",
		},
		{
			table:      &model.AuditLogEntry{},
			scope:      "guild_id = ? AND action IN ?",
			args:       []any{params.GuildID, model.ImportantAuditActions()},
			dimensions: []string{"action", "result", "source"},
			total:      &result.AuditTotal,
			buckets:    []*[]model.StatisticBucket{&result.AuditsByDay, &result.AuditsByAction, &result.AuditsByResult, &result.AuditsBySource},
			name:       "audit",
		},
	}
	for _, group := range groups {
		query := timeRange(s.db.WithContext(ctx).Model(group.table).Where(group.scope, group.args...), params)
		rows, err := aggregateStatistics(query, daySQL, dayArgs, group.dimensions)
		if err != nil {
			return nil, fmt.Errorf("derive %s statistics: %w", group.name, err)
		}
		counts := make([]map[string]int64, len(group.buckets))
		for i := range counts {
			counts[i] = make(map[string]int64)
		}
		for _, row := range rows {
			*group.total += row.Count
			keys := []string{row.Day, row.Dimension0, row.Dimension1, row.Dimension2}
			for i := range counts {
				counts[i][keys[i]] += row.Count
			}
		}
		for i, destination := range group.buckets {
			*destination = statisticBuckets(counts[i])
		}
	}
	return result, nil
}

// statisticsAggregate contains counts only; large source JSON, evidence, reasons,
// notification bodies and execution details never enter the statistics process.
type statisticsAggregate struct {
	Day        string
	Dimension0 string
	Dimension1 string
	Dimension2 string
	Count      int64
}

// aggregateStatistics scans one grouped projection per source so each source's
// totals and breakdowns share a query snapshot. Memory grows with distinct day
// and dimension combinations rather than the number or size of source records.
func aggregateStatistics(query *gorm.DB, daySQL string, dayArgs []any, dimensions []string) ([]statisticsAggregate, error) {
	selects := []string{daySQL + " AS day"}
	groups := []string{"day"}
	for i, expression := range dimensions {
		expression = "COALESCE(" + expression + ", '')"
		// Go previously grouped exact strings. MySQL's default case/accent-insensitive
		// collation must not merge distinct template identifiers or stored labels.
		if query.Dialector.Name() == "mysql" {
			expression = "CAST(" + expression + " AS BINARY)"
		}
		alias := fmt.Sprintf("dimension%d", i)
		selects = append(selects, expression+" AS "+alias)
		groups = append(groups, alias)
	}
	selects = append(selects, "COUNT(*) AS count")
	var rows []statisticsAggregate
	err := query.Select(strings.Join(selects, ", "), dayArgs...).Group(strings.Join(groups, ", ")).Scan(&rows).Error
	return rows, err
}

// statisticsDayExpression preserves UTC day labels from the former Go scan.
// SQLite normalizes encoded offsets. MySQL DATE fields are decoded in the DSN
// location; non-UTC locations therefore use driver-converted UTC day boundaries
// without relying on MySQL named timezone tables being installed. Public callers
// already constrain ranges to 366 days; the usual UTC DSN uses a simple SQL date.
func statisticsDayExpression(db *gorm.DB, params model.StaffStatisticsParams) (string, []any) {
	if db.Dialector.Name() != "mysql" {
		return "strftime('%Y-%m-%d', created_at)", nil
	}
	dialector, ok := db.Dialector.(*gormmysql.Dialector)
	if !ok || dialector.DSNConfig == nil || dialector.DSNConfig.Loc == nil || dialector.DSNConfig.Loc == time.UTC {
		return "DATE_FORMAT(created_at, '%Y-%m-%d')", nil
	}
	start := params.From.UTC()
	day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	var expression strings.Builder
	expression.WriteString("CASE")
	var args []any
	for day.Before(params.To.UTC()) {
		next := day.AddDate(0, 0, 1)
		expression.WriteString(" WHEN created_at < ? THEN ?")
		args = append(args, next, day.Format(time.DateOnly))
		day = next
	}
	expression.WriteString(" END")
	return expression.String(), args
}

// timeRange keeps the existing inclusive lower and exclusive upper boundary.
func timeRange(query *gorm.DB, params model.StaffStatisticsParams) *gorm.DB {
	return query.Where("created_at >= ? AND created_at < ?", params.From.UTC(), params.To.UTC())
}

// statisticBuckets preserves exact lexical ordering and non-nil empty arrays.
func statisticBuckets(counts map[string]int64) []model.StatisticBucket {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]model.StatisticBucket, 0, len(keys))
	for _, key := range keys {
		result = append(result, model.StatisticBucket{Key: key, Count: counts[key]})
	}
	return result
}
