package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// resolveInteractionGuildContext resolves interaction guild context from authoritative request and repository data.
func resolveInteractionGuildContext(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate) (*quack.GuildStaffContext, error) {
	userID, displayName, permissionBits := interactionMemberFields(interaction)
	return services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID,
		DiscordUserID:  userID,
		DisplayName:    displayName,
		PermissionBits: permissionBits,
		LastActiveAt:   time.Now().UTC(),
	})
}

// resolveTemplate resolves template from authoritative request and repository data.
func resolveTemplate(ctx context.Context, services *quack.Services, guildContext *quack.GuildStaffContext, templateInput string) (string, *quack.TemplateResponse, error) {
	value := strings.TrimSpace(templateInput)
	if value == "" {
		return "", nil, quack.ErrCaseValidation
	}

	templates, err := services.Templates.ListActive(ctx, guildContext)
	if err != nil {
		return "", nil, err
	}
	for _, template := range templates {
		if template.ID == value || strings.EqualFold(template.Slug, value) {
			matched := template
			return template.ID, &matched, nil
		}
	}

	return value, nil, nil
}

// handleTemplateAutocomplete handles template autocomplete and translates it into the package's application or response contract.
func handleTemplateAutocomplete(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil || services.Guilds.Authorize(ctx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord) != nil {
		return autocompleteResponse(nil)
	}

	data := interaction.ApplicationCommandData()
	add := data.GetOption("add")
	if add == nil {
		return autocompleteResponse(nil)
	}
	templateOption := add.GetOption("template")
	if templateOption == nil {
		return autocompleteResponse(nil)
	}

	query := strings.ToLower(strings.TrimSpace(templateOption.StringValue()))
	templates, err := services.Templates.ListActive(ctx, guildContext)
	if err != nil {
		slog.Error("failed to list templates for case autocomplete", "error", err)
		return autocompleteResponse(nil)
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, 25)
	for _, template := range templates {
		search := strings.ToLower(template.Slug + " " + template.Name + " " + template.Description)
		if query != "" && !strings.Contains(search, query) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  templateAutocompleteLabel(template),
			Value: template.ID,
		})
		if len(choices) == 25 {
			break
		}
	}

	return autocompleteResponse(choices)
}

// interactionMemberFields encapsulates the interaction member fields rule so callers share one consistent package implementation.
func interactionMemberFields(interaction *discordgo.InteractionCreate) (string, string, uint64) {
	if interaction == nil || interaction.Member == nil {
		return "", "", 0
	}

	member := interaction.Member
	userID := ""
	username := ""
	if member.User != nil {
		userID = member.User.ID
		username = member.User.Username
	}

	displayName := strings.TrimSpace(member.Nick)
	if displayName == "" && member.User != nil {
		displayName = strings.TrimSpace(member.User.GlobalName)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(username)
	}

	return userID, displayName, uint64(member.Permissions)
}

// optionStringValue encapsulates the option string value rule so callers share one consistent package implementation.
func optionStringValue(option *discordgo.ApplicationCommandInteractionDataOption) string {
	if option == nil || option.Value == nil {
		return ""
	}
	switch value := option.Value.(type) {
	case string:
		return value
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

// templateAutocompleteLabel encapsulates the template autocomplete label rule so callers share one consistent package implementation.
func templateAutocompleteLabel(template quack.TemplateResponse) string {
	name := strings.TrimSpace(template.Name)
	if name == "" {
		name = strings.TrimSpace(template.Slug)
	}
	description := strings.TrimSpace(template.Description)
	if description != "" {
		name = fmt.Sprintf("%s - %s", name, description)
	}
	return truncateDiscordChoiceName(name)
}

// truncateDiscordChoiceName encapsulates the truncate discord choice name rule so callers share one consistent package implementation.
func truncateDiscordChoiceName(value string) string {
	runes := []rune(value)
	if len(runes) <= 100 {
		return value
	}
	return string(runes[:100])
}

// caseCreateErrorMessage supplies creation-specific guidance only before a case is committed.
func caseCreateErrorMessage(err error) string {
	var denial *quack.AuthorizationError
	if errors.As(err, &denial) {
		return caseAuthorizationErrorMessage(denial)
	}
	switch {
	case errors.Is(err, quack.ErrCasePermissionDenied):
		return "No case was created. You need Moderate Members permission to create cases. Ask a staff member with that permission to handle this case."
	case errors.Is(err, quack.ErrAuthorizationDenied):
		return "No case was created. Your permissions or role position don’t allow this action."
	case errors.Is(err, quack.ErrAuthorizationUnavailable):
		return "I couldn’t check Discord permissions, so no case was created. Try again in a moment."
	default:
		return caseCommandErrorMessage(err)
	}
}

// caseCommandErrorMessage maps failures shared by reads and existing-case operations
// without claiming that creation was attempted or that a case does not exist.
func caseCommandErrorMessage(err error) string {
	switch {
	case errors.Is(err, quack.ErrCasePermissionDenied), errors.Is(err, quack.ErrAuthorizationDenied):
		return "You don’t have permission to do that. Ask a moderator with the required permission."
	case errors.Is(err, quack.ErrCaseTemplateNotAvailable):
		return "That rule is no longer available. Choose another from the suggestions."
	case errors.Is(err, quack.ErrCaseValidation):
		return "Something is missing or doesn’t look right. Check the case number and command options."
	case errors.Is(err, quack.ErrBotNotInGuild):
		return "I’m not set up in this server yet."
	default:
		slog.Error("case command failed", "error", err)
		return "I couldn’t finish that. Try again in a moment."
	}
}

// autocompleteResponse converts autocomplete response into its transport presentation without leaking transport types into the core.
func autocompleteResponse(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return ui.Autocomplete(choices)
}

// caseAuthorizationErrorMessage translates only known denial codes and permission
// bits into private recovery guidance; internal metadata never becomes reply text.
func caseAuthorizationErrorMessage(denial *quack.AuthorizationError) string {
	const prefix = "No case was created. "
	switch denial.Reason {
	case "permission_required", "bot_permission_required":
		permission := ""
		switch denial.RequiredPermission {
		case uint64(discordgo.PermissionModerateMembers):
			permission = "Moderate Members"
		case uint64(discordgo.PermissionKickMembers):
			permission = "Kick Members"
		case uint64(discordgo.PermissionBanMembers):
			permission = "Ban Members"
		}
		if permission != "" {
			if denial.Reason == "bot_permission_required" {
				return prefix + "Quack needs " + permission + " permission for the selected outcome. Ask a server administrator to update Quack's permissions, then try again."
			}
			return prefix + "You need " + permission + " for this outcome. Ask a staff member with that permission to handle it."
		}
	case "self_target":
		return prefix + "You cannot create a case against yourself. Select another member, or ask another authorized staff member to review your case."
	case "actor_hierarchy":
		return prefix + "The target's highest role is equal to or above yours. Ask a staff member with a higher role and the required permissions to handle this case."
	case "bot_hierarchy":
		return prefix + "The target's highest role is equal to or above Quack's. Ask a server administrator to review Quack's role position before trying again."
	case "bot_target":
		return prefix + "Cases cannot target bot accounts. Select a member who is not a bot."
	case "guild_owner_target":
		return prefix + "Cases cannot target the server owner. Select another member."
	case "target_not_in_guild":
		return prefix + "The target is no longer in this server. Select a current member."
	case "actor_not_in_guild":
		return prefix + "You are no longer a member of this server. Ask a current authorized staff member to handle this case."
	case "bot_not_in_guild":
		return prefix + "I’m not set up in this server yet. Ask a server administrator to restore Quack before trying again."
	}
	return prefix + "Quack could not confirm authority for this case. Ask a server administrator to review your permissions and the target, then try again."
}
