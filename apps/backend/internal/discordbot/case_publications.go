package discordbot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CasePublicationRepository exposes system-owned transport bookkeeping and
// minimal persisted case state; refreshing never authorizes moderator actions.
type CasePublicationRepository interface {
	ListDueCasePublications(context.Context, time.Time, int) ([]model.CasePublication, error)
	CompleteCasePublicationRefresh(context.Context, string, uint64, string, time.Time, bool) error
	DeleteCasePublication(context.Context, string) error
	GetCaseByID(context.Context, string) (*model.Case, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	CasePublicationEvidenceIncomplete(context.Context, string) (bool, error)
}

// RunCasePublications refreshes persisted public receipts until process shutdown.
// Edits target existing messages, so retries and concurrent bot processes cannot
// create duplicate messages. Terminal receipts sleep until a committed source
// mutation requests refresh; pending work and recoverable failures remain due.
func (b *Bot) RunCasePublications(ctx context.Context, repository CasePublicationRepository) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if err := refreshCasePublications(ctx, repository, b.editCasePublication, time.Now().UTC()); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Case publication refresh failed", "error_type", fmt.Sprintf("%T", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// editCasePublication uses bot credentials so refresh survives interaction expiry.
func (b *Bot) editCasePublication(ctx context.Context, receipt model.CasePublication, message ui.Message) error {
	params := message.SendParams(ui.SessionApplicationID(b.Session))
	_, err := b.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: receipt.MessageID, Channel: receipt.ChannelID, Content: &params.Content, Embeds: &params.Embeds, Components: &params.Components, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// refreshCasePublications retries all recoverable failures and retires only
// explicit unknown-message responses or deleted case records. Its rendering uses
// the saved public display plus current validity and enforcement statuses.
func refreshCasePublications(ctx context.Context, repository CasePublicationRepository, edit func(context.Context, model.CasePublication, ui.Message) error, now time.Time) error {
	receipts, err := repository.ListDueCasePublications(ctx, now, 50)
	if err != nil {
		return err
	}
	var failures []error
	for _, receipt := range receipts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		digest, delay, missing, refreshErr := refreshCasePublication(ctx, repository, receipt, edit)
		if refreshErr != nil {
			failures = append(failures, refreshErr)
			digest, delay = receipt.LastDigest, 30*time.Second
		}
		if missing {
			err = repository.DeleteCasePublication(ctx, receipt.MessageID)
		} else {
			err = repository.CompleteCasePublicationRefresh(ctx, receipt.MessageID, receipt.Revision, digest, now.Add(delay), delay > 0)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// refreshCasePublication builds only the allowlisted public view; action configs,
// private reasons, evidence and moderator identity never enter the renderer.
func refreshCasePublication(ctx context.Context, repository CasePublicationRepository, receipt model.CasePublication, edit func(context.Context, model.CasePublication, ui.Message) error) (digest string, delay time.Duration, missing bool, err error) {
	var presentation views.CaseCreated
	if err = json.Unmarshal([]byte(receipt.PresentationJSON), &presentation); err != nil {
		return
	}
	if presentation.Case == nil || presentation.Case.ID != receipt.CaseID {
		err = errors.New("invalid case publication snapshot")
		return
	}
	var item *model.Case
	item, err = repository.GetCaseByID(ctx, receipt.CaseID)
	if err != nil {
		return
	}
	if item == nil {
		missing = true
		return
	}
	presentation.Case.Validity = item.Validity
	presentation.Case.EvidenceIncomplete, err = repository.CasePublicationEvidenceIncomplete(ctx, receipt.CaseID)
	if err != nil {
		return
	}
	var actions []model.CaseActionExecution
	actions, err = repository.ListCaseActionExecutions(ctx, receipt.CaseID)
	if err != nil {
		return
	}
	presentation.Case.Actions = nil
	delay = 0
	for _, action := range actions {
		presentation.Case.Actions = append(presentation.Case.Actions, quack.CaseActionResponse{ID: action.ID, ActionType: action.ActionType, Status: action.Status})
		switch action.Status {
		case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying:
			delay = 2 * time.Second
		}
	}
	message := views.CaseCreatedMessage(presentation)
	if item.Validity == model.CaseValidityVoided {
		message.Content += "\n\nThis case has been voided."
	}
	var encoded []byte
	encoded, err = json.Marshal(message)
	if err != nil {
		return
	}
	digest = fmt.Sprintf("%x", sha256.Sum256(encoded))
	if digest == receipt.LastDigest {
		return
	}
	err = edit(ctx, receipt, message)
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == discordgo.ErrCodeUnknownMessage {
		missing, err = true, nil
	}
	return
}
