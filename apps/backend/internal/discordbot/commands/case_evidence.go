package commands

import (
	"context"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// handleCaseEvidenceComponent shows captured content and context in the staff channel.
func handleCaseEvidenceComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		detail, err := ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parsed.Payload, 1)
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseWebLink(views.CaseEvidenceSnapshotPage(detail, 1, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
		return err
	})
}

// pageEvidence reloads the case through live staff authorization on every click;
// component payloads carry navigation only, never captured content or authority.
func pageEvidence(delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		coordinates := strings.SplitN(parts[0], ":", 2)
		position, page := 1, 1
		if len(coordinates) == 1 {
			page, err = strconv.Atoi(coordinates[0])
		} else {
			position, err = strconv.Atoi(coordinates[0])
			if err == nil {
				page, err = strconv.Atoi(coordinates[1])
			}
		}
		if err != nil || position < 1 || position > 1000000 || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				return err
			}
			detail, err := ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], position)
			if err != nil {
				return err
			}
			applicationID := ui.SessionApplicationID(ctx.Session)
			last := len(views.CaseEvidenceSnapshotPages(detail, applicationID))
			if page > last {
				page = last
			}
			page += delta
			if page < 1 && detail.Position > 1 {
				detail, err = ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], detail.Position-1)
				page = 1000000
			} else if page > last && int64(detail.Position) < detail.Total {
				detail, err = ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], detail.Position+1)
				page = 1
			}
			if err != nil {
				return err
			}
			_, err = responder.UpdateMessage(ui.EditMessage(caseWebLink(views.CaseEvidenceSnapshotPage(detail, page, applicationID), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
			return err
		})
	}
}

// pageCaseRecord shares navigation and authorization for staff case record views.
func pageCaseRecord(delta int, render func(*quack.CaseDetailResponse, int, string) ui.Message) ui.Handler {
	return pageCaseRecordWithLoader(delta, render, (*quack.CaseService).GetNativeDetail)
}

// caseRecordLoader selects only the authorized data required by a native view.
type caseRecordLoader func(*quack.CaseService, context.Context, *quack.GuildStaffContext, string) (*quack.CaseDetailResponse, error)

// pageCaseRecordWithLoader retains live authority and navigation semantics while
// evidence pages avoid loading unrelated parts of the staff case record.
func pageCaseRecordWithLoader(delta int, render func(*quack.CaseDetailResponse, int, string) ui.Message, load caseRecordLoader) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		page, err := strconv.Atoi(parts[0])
		if err != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				return err
			}
			detail, err := load(ctx.Services.Cases, taskCtx, guild, parts[1])
			if err != nil {
				return err
			}
			_, err = responder.UpdateMessage(ui.EditMessage(caseWebLink(render(detail, page+delta, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
			return err
		})
	}
}

// handleCaseUserComponent opens member history from a case in the invoking channel.
func handleCaseUserComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That user button is invalid."))
	}
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		profile, err := ctx.Services.Cases.UserHistory(taskCtx, guild, parsed.Payload, quack.CaseListInput{Limit: "10"})
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseWebLink(views.CaseProfileMessage(profile, 1, parsed.Payload), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "members", parsed.Payload)))
		return err
	})
}
