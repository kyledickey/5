package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestCaseCreatePermissionGuidance verifies safe recovery copy survives wrapped
// denials without misidentifying target safety failures as missing permissions.
func TestCaseCreatePermissionGuidance(t *testing.T) {
	tests := []struct {
		name, reason string
		permission   uint64
		want         string
	}{
		{"missing basic authority", "permission_required", uint64(discordgo.PermissionModerateMembers), "You need Moderate Members"},
		{"missing kick", "permission_required", uint64(discordgo.PermissionKickMembers), "You need Kick Members"},
		{"missing ban", "permission_required", uint64(discordgo.PermissionBanMembers), "You need Ban Members"},
		{"bot missing ban", "bot_permission_required", uint64(discordgo.PermissionBanMembers), "Quack needs Ban Members"},
		{"self", "self_target", 0, "cannot create a case against yourself"},
		{"actor hierarchy", "actor_hierarchy", 0, "equal to or above yours"},
		{"bot hierarchy", "bot_hierarchy", 0, "equal to or above Quack's"},
		{"owner", "guild_owner_target", 0, "cannot target the server owner"},
		{"unknown", "private-internal-reason", 0, "could not confirm authority"},
		{"unknown permission", "permission_required", 12345, "could not confirm authority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("private adapter failure: %w", &quack.AuthorizationError{Reason: tt.reason, RequiredPermission: tt.permission, MetadataJSON: "private-metadata"})
			got := caseCreateErrorMessage(err)
			if !strings.HasPrefix(got, "No case was created.") || !strings.Contains(got, tt.want) {
				t.Fatalf("unexpected guidance: %s", got)
			}
			if strings.Contains(got, "private") || strings.Contains(got, "12345") {
				t.Fatalf("internal details leaked: %s", got)
			}
			if tt.reason == "permission_required" && tt.permission != 12345 && !strings.Contains(got, "Ask a staff member with that permission") {
				t.Fatalf("missing recovery guidance: %s", got)
			}
			if tt.permission == 0 && strings.Contains(got, "Ban Members") {
				t.Fatalf("invented missing permission: %s", got)
			}
		})
	}
}

// TestExistingCasePermissionErrorsDoNotClaimCreation keeps reads, edits and
// reversals from claiming that no case exists when a shared error is mapped.
func TestExistingCasePermissionErrorsDoNotClaimCreation(t *testing.T) {
	got := caseCommandErrorMessage(&quack.AuthorizationError{Reason: "permission_required", RequiredPermission: uint64(discordgo.PermissionBanMembers)})
	if got != "You don’t have permission to do that. Ask a moderator with the required permission." {
		t.Fatalf("unexpected existing-case error: %s", got)
	}
}
