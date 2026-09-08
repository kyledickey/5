package discordbot

import (
	"fmt"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
)

// renderCaseNotification describes the rule and recorded outcome without staff
// context, evidence or moderator identity.
func renderCaseNotification(request quack.CaseNotificationRequest) string {
	guildName := "this server"
	if strings.TrimSpace(request.GuildName) != "" {
		guildName = request.GuildName
	}
	server := "**" + discordtext.Plain(guildName) + "**"
	icon, lead := "warn", "You received a warning in "+server
	primary := -1
	removed := false
	for i, action := range request.Outcomes {
		if action.Status != model.ActionExecutionSucceeded {
			continue
		}
		switch action.ActionType {
		case model.ActionTimeoutUser:
			icon, lead = "timeout", "You’ve been timed out in "+server
		case model.ActionKickUser:
			icon, lead = "kick", "You’ve been removed from "+server
		case model.ActionBanUser:
			icon, lead = "ban", "You’ve been banned from "+server
		case model.ActionRemoveTimeout:
			icon, lead, removed = "untimeout", "Your timeout in "+server+" has ended", true
		case model.ActionUnbanUser:
			icon, lead, removed = "unban", "You’re no longer banned from "+server, true
		default:
			continue
		}
		primary = i
		break
	}
	parts := []string{}
	if strings.TrimSpace(request.RuleName) != "" {
		rule := "**" + discordtext.Plain(request.RuleName) + "**"
		if removed {
			parts = append(parts, "This updates your case for "+rule+".")
		} else {
			lead += " for " + rule
		}
	}
	if strings.TrimSpace(request.Introduction) != "" {
		parts = append(parts, discordtext.Plain(strings.TrimSpace(request.Introduction)))
	}
	for i, action := range request.Outcomes {
		if action.Status == model.ActionExecutionFailed && primary == -1 {
			icon = "error"
		}
		if i != primary && action.ActionType != model.ActionSendDM {
			parts = append(parts, discordtext.ActionSentence(action.ActionType, action.Status))
		}
		if action.Status != model.ActionExecutionSucceeded || action.ActionType != model.ActionTimeoutUser {
			continue
		}
		if action.TimeoutUntil != nil {
			until := action.TimeoutUntil
			parts = append(parts, fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.", until.Unix(), until.Unix()))
		}
	}
	if request.IncludeAppealInstructions {
		parts = append(parts, "Use the Appeal decision button below to ask the moderators to review this case.")
	}
	if strings.TrimSpace(request.Footer) != "" {
		parts = append(parts, discordtext.Plain(strings.TrimSpace(request.Footer)))
	}
	meta := fmt.Sprintf("Case #%d", request.CaseNumber)
	if !request.CreatedAt.IsZero() {
		meta += fmt.Sprintf(" · <t:%d:R>", request.CreatedAt.Unix())
	}
	return discordtext.Conversation(icon, lead+".", discordtext.Plain(request.Reason), strings.Join(parts, "\n\n"), meta)
}
