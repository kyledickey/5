package quack_test

import "context"

// RemoveOwnedTimeout supplies the checked capability in orchestration fixtures;
// dedicated adapter tests exercise actual live-state ownership checks.
func (f *fakeEnforcementClient) RemoveOwnedTimeout(ctx context.Context, guildID, userID, expected, reason string) (map[string]any, error) {
	return f.RemoveMemberTimeout(ctx, guildID, userID, reason)
}

// RemoveOwnedBan supplies checked ban reversal in existing permission fixtures.
func (f *fakeEnforcementClient) RemoveOwnedBan(ctx context.Context, guildID, userID, expected, reason string) (map[string]any, error) {
	return f.UnbanMember(ctx, guildID, userID, reason)
}
