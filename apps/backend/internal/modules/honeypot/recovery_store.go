package honeypot

import (
	"context"
	"time"
)

// incidentLeaseDuration allows ordinary case preflight to finish before recovery
// takes ownership. UpdatedAt is a persisted lease epoch, avoiding schema drift.
const incidentLeaseDuration = time.Minute

// claimPendingIncident leases one expired pending incident with an atomic epoch
// comparison. Primary and recovery completion both fence against this epoch.
func (s *Store) claimPendingIncident(ctx context.Context) (*Trigger, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	var candidates []Trigger
	if err := s.db.WithContext(ctx).Where("outcome = ? AND updated_at <= ?", OutcomePending, now.Add(-incidentLeaseDuration)).Order("updated_at ASC").Limit(10).Find(&candidates).Error; err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		result := s.db.WithContext(ctx).Model(&Trigger{}).Where("id = ? AND outcome = ? AND updated_at = ?", candidate.ID, OutcomePending, candidate.UpdatedAt).Update("updated_at", now)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			candidate.UpdatedAt = now
			return &candidate, nil
		}
	}
	return nil, nil
}

// completeIncident fences a saved case or terminal failure against the current
// attempt's lease. A timed-out original worker cannot overwrite its recovery.
func (s *Store) completeIncident(ctx context.Context, trigger *Trigger, outcome Outcome, caseID, failureCode string) error {
	result := s.db.WithContext(ctx).Model(&Trigger{}).Where("id = ? AND outcome = ? AND updated_at = ?", trigger.ID, OutcomePending, trigger.UpdatedAt).
		Updates(map[string]any{"outcome": outcome, "case_id": caseID, "failure_code": failureCode, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDuplicate
	}
	return nil
}
