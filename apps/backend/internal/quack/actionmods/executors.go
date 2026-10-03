package actionmods

import (
	"context"
	"fmt"
)

// SendDM sends the template-provided message, falling back to the case reason.
func SendDM(client DiscordClient) Executor {
	return Func(func(ctx context.Context, action Context) Result {
		if client == nil {
			return PermanentError("discord_unavailable", "discord action client is not configured")
		}
		message := configString(action.Config, "message")
		if message == "" {
			message = fmt.Sprintf("You received a moderation case in this server: %s", action.Case.Reason)
		}
		return discordResult(client.SendDM(ctx, action.Case.TargetDiscordUserID, message))
	})
}

// TimeoutUser applies the exact template-owned duration.
func TimeoutUser(client DiscordClient) Executor {
	return enforce(client, func(ctx context.Context, enforcement EnforcementClient, action Context) Result {
		duration := configInt(action.Config, "duration_seconds")
		if duration <= 0 {
			return PermanentError("invalid_action_config", "timeout duration is missing")
		}
		return discordResult(enforcement.TimeoutMember(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, duration, AuditReason(action)))
	})
}

func KickUser(client DiscordClient) Executor {
	return enforce(client, func(ctx context.Context, enforcement EnforcementClient, action Context) Result {
		return discordResult(enforcement.KickMember(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, AuditReason(action)))
	})
}

// BanUser applies the exact Discord-supported history deletion window.
func BanUser(client DiscordClient) Executor {
	return enforce(client, func(ctx context.Context, enforcement EnforcementClient, action Context) Result {
		deleteMessageSeconds := configInt(action.Config, "delete_message_seconds")
		return discordResult(enforcement.BanMember(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, deleteMessageSeconds, AuditReason(action)))
	})
}

func RemoveTimeout(client DiscordClient) Executor {
	return enforce(client, func(ctx context.Context, enforcement EnforcementClient, action Context) Result {
		return discordResult(enforcement.RemoveMemberTimeout(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, AuditReason(action)))
	})
}

func UnbanUser(client DiscordClient) Executor {
	return enforce(client, func(ctx context.Context, enforcement EnforcementClient, action Context) Result {
		return discordResult(enforcement.UnbanMember(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, AuditReason(action)))
	})
}

// enforce runs a moderation call only once the adapter is known to support
// enforcement. An adapter without that capability fails permanently rather than
// consuming a retry against a client that can never perform the operation.
func enforce(client DiscordClient, call func(context.Context, EnforcementClient, Context) Result) Executor {
	return Func(func(ctx context.Context, action Context) Result {
		enforcement, ok := client.(EnforcementClient)
		if !ok {
			return PermanentError("discord_unavailable", "Discord enforcement is not configured")
		}
		return call(ctx, enforcement, action)
	})
}

func discordResult(response map[string]any, err error) Result {
	if err != nil {
		return ResultFromError(err)
	}
	return Result{Response: response}
}
