package discordtext

import "github.com/quackdiscord/bot/internal/quack/model"

// ActionSentence makes enforcement status explicit without exposing internal enum names.
func ActionSentence(action model.ActionType, status model.ActionExecutionStatus) string {
	name := action.Label()
	switch status {
	case model.ActionExecutionSucceeded:
		switch action {
		case model.ActionTimeoutUser:
			return "Timeout applied."
		case model.ActionKickUser:
			return "Member kicked."
		case model.ActionBanUser:
			return "Member banned."
		case model.ActionRemoveTimeout:
			return "Member is no longer timed out."
		case model.ActionUnbanUser:
			return "Member is no longer banned."
		case model.ActionSendDM:
			return "Message sent."
		}
		return name + " completed."
	case model.ActionExecutionPending:
		return name + " queued."
	case model.ActionExecutionRunning:
		return name + " in progress."
	case model.ActionExecutionRetrying:
		return name + " will be retried."
	case model.ActionExecutionFailed:
		return name + " couldn’t be completed. Staff review needed."
	case model.ActionExecutionSkipped:
		return name + " skipped."
	case model.ActionExecutionCancelled:
		return name + " cancelled."
	default:
		return name + " status is unavailable."
	}
}
