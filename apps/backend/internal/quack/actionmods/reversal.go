package actionmods

import "context"

// RemoveTimeout executes a permission-checked timeout reversal.
func RemoveTimeout(client DiscordClient) Executor {
	return Func(func(ctx context.Context, action Context) Result {
		enforcement, ok := client.(EnforcementClient)
		if !ok {
			return PermanentError("discord_unavailable", "Discord enforcement is not configured")
		}
		response, err := enforcement.RemoveMemberTimeout(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, AuditReason(action))
		if err != nil {
			return ResultFromError(err)
		}
		return Result{Response: response}
	})
}

// UnbanUser executes a permission-checked ban reversal.
func UnbanUser(client DiscordClient) Executor {
	return Func(func(ctx context.Context, action Context) Result {
		enforcement, ok := client.(EnforcementClient)
		if !ok {
			return PermanentError("discord_unavailable", "Discord enforcement is not configured")
		}
		response, err := enforcement.UnbanMember(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, AuditReason(action))
		if err != nil {
			return ResultFromError(err)
		}
		return Result{Response: response}
	})
}
