package honeypot

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// IncidentRecoverer separates query-first reconciliation from live preparation
// for a case that was never saved. Neither lookup nor preparation applies policy.
type IncidentRecoverer interface {
	FindHoneypotCase(context.Context, ApplyRequest) (ApplyResult, error)
	PrepareHoneypotRecovery(context.Context, ApplyRequest) (ApplyRequest, error)
}

// RecoverPending reconciles expired incident leases with the normal case path.
// Saved cases are adopted before current permissions/configuration are consulted;
// absent cases require fresh policy and member checks and reuse the original key.
// Returned guild IDs need only presentation refresh, never another punishment.
func (s *Service) RecoverPending(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("honeypot recovery is not configured")
	}
	recovery, ok := s.applier.(IncidentRecoverer)
	if !ok {
		return nil, nil
	}
	var guilds []string
	var failures []error
	for range min(max(limit, 1), 8) {
		trigger, err := s.store.claimPendingIncident(ctx)
		if err != nil {
			return guilds, errors.Join(append(failures, err)...)
		}
		if trigger == nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		recovered, err := s.recoverIncident(attemptCtx, recovery, trigger)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
		if recovered {
			guilds = append(guilds, trigger.GuildID)
		}
	}
	return guilds, errors.Join(failures...)
}

// recoverIncident keeps the saved-case lookup ahead of any live dependency. A
// transient failure leaves the leased incident pending for a later restart/tick.
func (s *Service) recoverIncident(ctx context.Context, recovery IncidentRecoverer, trigger *Trigger) (bool, error) {
	request := ApplyRequest{GuildID: trigger.GuildID, TemplateID: trigger.TemplateID, TargetDiscordUserID: trigger.TargetDiscordUserID,
		ContextChannelDiscordID: trigger.ChannelDiscordID, ContextMessageDiscordID: trigger.MessageDiscordID,
		IdempotencyKey: "honeypot:" + trigger.GuildID + ":" + trigger.MessageDiscordID, Source: SourceHoneypot, ActorType: ActorTypeSystem}
	result, err := recovery.FindHoneypotCase(ctx, request)
	if err != nil {
		return false, err
	}
	if result.CaseID == "" {
		settings, enabled, err := s.loadSettings(ctx, trigger.GuildID)
		if err != nil {
			return false, err
		}
		if !enabled || settings.ChannelDiscordID != trigger.ChannelDiscordID || settings.TemplateID != trigger.TemplateID {
			return false, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "recovery_policy_changed")
		}
		if s.templates == nil || s.channels == nil {
			return false, errors.New("honeypot recovery validators are unavailable")
		}
		if err := s.templates.ValidateHoneypotTemplate(ctx, trigger.GuildID, trigger.TemplateID); err != nil {
			if errors.Is(err, ErrTemplateUnavailable) {
				return false, errors.Join(err, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "template_unavailable"))
			}
			return false, err
		}
		if err := s.channels.ValidateHoneypotChannel(ctx, trigger.GuildID, trigger.ChannelDiscordID); err != nil {
			return false, err
		}
		request, err = recovery.PrepareHoneypotRecovery(ctx, request)
		if err != nil {
			if errors.Is(err, ErrExempt) {
				return false, s.store.completeIncident(ctx, trigger, OutcomeExempt, "", "recovery_author_exempt")
			}
			if errors.Is(err, ErrNotTrigger) {
				return false, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "recovery_source_unavailable")
			}
			return false, err
		}
		result, err = s.applier.ApplyHoneypotCase(ctx, request)
		if err != nil {
			return false, err
		}
		if result.CaseID == "" {
			return false, fmt.Errorf("recovered honeypot application returned no saved case")
		}
	}
	if err := s.store.completeIncident(ctx, trigger, OutcomeCreated, result.CaseID, ""); err != nil {
		return false, err
	}
	s.audit(ctx, trigger.GuildID, "", "honeypot.case.created", "case", "success", nil, result.CaseID)
	return true, nil
}
