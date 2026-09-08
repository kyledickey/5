package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// DeriveStaffStatistics calculates operational counts directly from immutable source records.
func (s *Store) DeriveStaffStatistics(ctx context.Context, params model.StaffStatisticsParams) (*model.StaffStatistics, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if params.GuildID == "" || params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("invalid staff statistics range")
	}

	var cases []model.Case
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID), params).Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("derive case statistics: %w", err)
	}
	var actions []model.CaseActionExecution
	if err := timeRange(s.db.WithContext(ctx).Where("case_id IN (SELECT id FROM cases WHERE guild_id = ?)", params.GuildID), params).Find(&actions).Error; err != nil {
		return nil, fmt.Errorf("derive action statistics: %w", err)
	}
	var appeals []model.Appeal
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID), params).Find(&appeals).Error; err != nil {
		return nil, fmt.Errorf("derive appeal statistics: %w", err)
	}
	var audits []model.AuditLogEntry
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID).Where("action IN ?", model.ImportantAuditActions()), params).Find(&audits).Error; err != nil {
		return nil, fmt.Errorf("derive audit statistics: %w", err)
	}

	result := &model.StaffStatistics{From: params.From.UTC(), To: params.To.UTC(), CaseTotal: int64(len(cases)), ActionTotal: int64(len(actions)), AppealTotal: int64(len(appeals)), AuditTotal: int64(len(audits))}
	caseDays, caseTemplates, caseValidity, caseSources := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range cases {
		caseDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		template := "historical_or_deleted"
		if item.TemplateID != nil && *item.TemplateID != "" {
			template = *item.TemplateID
		}
		caseTemplates[template]++
		caseValidity[string(item.Validity)]++
		caseSources[string(item.Source)]++
	}
	actionDays, actionTypes, actionResults := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range actions {
		actionDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		actionTypes[string(item.ActionType)]++
		actionResults[string(item.Status)]++
	}
	appealDays, appealStatuses := map[string]int64{}, map[string]int64{}
	for _, item := range appeals {
		appealDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		appealStatuses[string(item.Status)]++
	}
	auditDays, auditActions, auditResults, auditSources := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range audits {
		auditDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		auditActions[item.Action]++
		auditResults[string(item.Result)]++
		auditSources[string(item.Source)]++
	}
	result.CasesByDay = statisticBuckets(caseDays)
	result.CasesByTemplate = statisticBuckets(caseTemplates)
	result.CasesByValidity = statisticBuckets(caseValidity)
	result.CasesBySource = statisticBuckets(caseSources)
	result.ActionsByDay = statisticBuckets(actionDays)
	result.ActionsByType = statisticBuckets(actionTypes)
	result.ActionsByResult = statisticBuckets(actionResults)
	result.AppealsByDay = statisticBuckets(appealDays)
	result.AppealsByStatus = statisticBuckets(appealStatuses)
	result.AuditsByDay = statisticBuckets(auditDays)
	result.AuditsByAction = statisticBuckets(auditActions)
	result.AuditsByResult = statisticBuckets(auditResults)
	result.AuditsBySource = statisticBuckets(auditSources)
	return result, nil
}

func timeRange(query *gorm.DB, params model.StaffStatisticsParams) *gorm.DB {
	return query.Where("created_at >= ? AND created_at < ?", params.From.UTC(), params.To.UTC())
}

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
