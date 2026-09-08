package tickets

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// DiscordClient is the narrow private-channel transport owned by the ticket adapter.
type DiscordClient interface {
	CreatePrivateTicketChannel(context.Context, string, string, Settings) (string, error)
	EnsureTicketPermissions(context.Context, string, string, string) error
	SendTicketReply(context.Context, string, string) error
	SendTicketWelcome(context.Context, *Ticket) error
	JoinTicketThread(context.Context, string, string) error
	FreezeTicketChannel(context.Context, string) error
	CaptureTicketTranscript(context.Context, string) (string, error)
	PublishTicketQueue(context.Context, *Ticket, Settings, *Transcript) (*QueueReceipt, error)
	DeleteTicketChannel(context.Context, string) error
	DeleteProvisionalTicketChannel(context.Context, string) error
}

// DiscordAdapter translates Discord entry/components into ticket service operations.
type DiscordAdapter struct {
	service *Service
	client  DiscordClient
	closes  ticketCloseLocks
}

// NewDiscordAdapter constructs the ticket Discord integration without central command registration.
func NewDiscordAdapter(service *Service, client DiscordClient) *DiscordAdapter {
	return &DiscordAdapter{service: service, client: client}
}

// Open provisions a private thread/channel and creates the matching backend ticket.
func (a *DiscordAdapter) Open(ctx context.Context, actor Actor) (*Ticket, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	settings, enabled, err := a.service.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	if settings.QueueChannelDiscordID == "" {
		return nil, errors.New("ticket queue channel is not configured")
	}
	token, err := a.service.store.reserveOpening(ctx, actor, a.service.now())
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if err := a.service.store.releaseOpening(cleanupCtx, actor, token); err != nil {
			slog.ErrorContext(cleanupCtx, "Ticket opening reservation cleanup failed", "guild_id", actor.GuildID, "ticket_id", token)
		}
	}()
	channelID, err := a.client.CreatePrivateTicketChannel(ctx, actor.GuildID, actor.DiscordUserID, settings)
	if err != nil {
		return nil, err
	}
	if err := a.client.EnsureTicketPermissions(ctx, channelID, actor.DiscordUserID, actor.GuildID); err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cleanupCancel()
		_ = a.client.DeleteProvisionalTicketChannel(cleanupCtx, channelID)
		return nil, err
	}
	ticket, err := a.service.store.finishOpening(ctx, actor, token, channelID, a.service.now())
	if err != nil {
		// A failed commit acknowledgement may conceal a committed ticket. Keep
		// its private channel instead of deleting a potentially accepted ticket.
		a.service.audit(ctx, actor, "ticket.open", token, "failure", err)
		return nil, err
	}
	a.service.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	// A failed greeting must not suppress the staff notification for a saved ticket.
	welcomeErr := a.client.SendTicketWelcome(ctx, ticket)
	_, queueErr := a.publishQueue(ctx, ticket, settings, nil)
	return ticket, errors.Join(welcomeErr, queueErr)
}

// Reply sends a private Discord message only after backend authorization succeeds.
func (a *DiscordAdapter) Reply(ctx context.Context, actor Actor, ticketID, body string) error {
	if err := validateReply(body); err != nil {
		return err
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if err := a.client.SendTicketReply(ctx, ticket.ThreadDiscordChannelID, body); err != nil {
		return err
	}
	return a.service.Reply(ctx, actor, ticketID, body)
}

// Close preserves and publishes the transcript before deleting the private thread.
func (a *DiscordAdapter) Close(ctx context.Context, actor Actor, ticketID string) (*Ticket, error) {
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+ticketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	resolved := ticket
	if ticket.Status == StatusOpen {
		if err := a.client.FreezeTicketChannel(ctx, ticket.ThreadDiscordChannelID); err != nil {
			return nil, err
		}
		transcript, err := a.client.CaptureTicketTranscript(ctx, ticket.ThreadDiscordChannelID)
		if err != nil {
			return nil, err
		}
		resolved, err = a.service.Resolve(ctx, actor, ticketID, transcript)
		if err != nil {
			return nil, err
		}
	} else if ticket.Status != StatusResolved {
		return nil, ErrInvalidTransition
	}
	if resolved.TranscriptURL == "" {
		transcript, err := a.service.Transcript(ctx, actor, ticketID)
		if err != nil {
			return resolved, err
		}
		settings, _, err := a.service.loadSettings(ctx, actor.GuildID)
		if err != nil {
			return resolved, err
		}
		if _, err := a.publishQueue(ctx, resolved, settings, transcript); err != nil {
			return resolved, err
		}
	}
	if err := a.client.DeleteTicketChannel(ctx, ticket.ThreadDiscordChannelID); err != nil {
		return resolved, err
	}

	return resolved, nil
}

// Cancel uses the same transcript-preserving closure as the Close control.
func (a *DiscordAdapter) Cancel(ctx context.Context, actor Actor, ticketID string) (*Ticket, error) {
	return a.Close(ctx, actor, ticketID)
}

// publishQueue persists a successful queue send/edit before source cleanup.
func (a *DiscordAdapter) publishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) (string, error) {
	if settings.QueueChannelDiscordID == "" {
		return "", errors.New("ticket queue channel is not configured")
	}
	receipt, err := a.client.PublishTicketQueue(ctx, ticket, settings, transcript)
	if err != nil {
		return "", err
	}
	if receipt == nil || receipt.MessageID == "" {
		return "", errors.New("ticket queue delivery returned no message")
	}
	url := ""
	if transcript != nil {
		url = receipt.URL
		if url == "" {
			return "", errors.New("ticket transcript delivery returned no link")
		}
	}
	if err := a.service.store.saveQueueReceipt(ctx, ticket, settings.QueueChannelDiscordID, receipt.MessageID, url); err != nil {
		return "", err
	}
	return receipt.MessageID, nil
}

// RepairPermissions restores the exact member/staff-only ACL and records the repair.
func (a *DiscordAdapter) RepairPermissions(ctx context.Context, actor Actor, ticketID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	_, enabled, err := a.service.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	if err := a.client.EnsureTicketPermissions(ctx, ticket.ThreadDiscordChannelID, ticket.OwnerDiscordUserID, actor.GuildID); err != nil {
		return err
	}
	return a.service.RecordPermissionsRepaired(ctx, actor.GuildID, ticketID)
}

// HandleDeletedChannel records a recoverable missing-channel state without exposing or recreating transcript content.
func (a *DiscordAdapter) HandleDeletedChannel(ctx context.Context, guildID, ticketID, channelID string) error {
	if a == nil || a.service == nil {
		return errors.New("ticket Discord adapter is not configured")
	}
	return a.service.RecordChannelMissing(ctx, guildID, ticketID, channelID)
}

// HandleDeletedEntryChannel disables new tickets until an administrator selects a private entry destination.
func (a *DiscordAdapter) HandleDeletedEntryChannel(ctx context.Context, guildID, channelID string) error {
	return a.service.RepairDeletedEntryChannel(ctx, guildID, channelID)
}

// Join admits a current moderator to an open ticket without assigning ownership.
// The caller supplies freshly resolved guild authority, never cached role grants.
func (a *DiscordAdapter) Join(ctx context.Context, actor Actor, ticketID string) error {
	if !actor.CanModerate {
		return ErrPermissionDenied
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusOpen {
		return ErrInvalidTransition
	}
	return a.client.JoinTicketThread(ctx, ticket.ThreadDiscordChannelID, actor.DiscordUserID)
}
