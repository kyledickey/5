package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordtext"
)

// previewSample holds deliberately written sample copy without invoking a domain service.
// All records, conversations, and controls are synthetic, including the DM examples.
type previewSample struct {
	name, group, icon, title, lead, detail, quote, meta string
	buttons                                             []string
}

// previewIconKeys keeps the gallery and the upload completeness check in sync.
var previewIconKeys = strings.Fields("warn ban timeout kick unban untimeout shield case case_add case_void history note evidence search appeal review accept decline message reply ticket success error info pending running retry member join leave lock unlock delete edit link calendar pin settings spark duck")

// UIPreviewCommandSpec exposes a development-only gallery with no moderation dependencies.
func UIPreviewCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionManageServer)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{
		Name: "ui-preview", Description: "Compare new text messages with Quack's custom icons", DefaultMemberPermissions: &permissions, DMPermission: &dm,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "style", Description: "Pick a text design direction", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "All three text designs", Value: "all"}, {Name: "A · Compact", Value: "compact"}, {Name: "B · Conversation", Value: "conversation"}, {Name: "C · Spotlight", Value: "spotlight"},
			}},
			{Type: discordgo.ApplicationCommandOptionString, Name: "section", Description: "Compare a smaller group, or browse the icon set", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Every message", Value: "all"}, {Name: "Cases and moderation", Value: "cases"}, {Name: "Punishment DMs", Value: "dms"}, {Name: "Appeals", Value: "appeals"}, {Name: "Tickets", Value: "tickets"}, {Name: "Activity and errors", Value: "activity"}, {Name: "All 40 icons", Value: "icons"},
			}},
		},
	}, Handler: handleUIPreview}
}

// handleUIPreview loads only the invoking application's emoji catalog and posts
// inert samples in the invoking channel. It never sends DMs or changes records.
func handleUIPreview(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil || ctx.Session == nil || ctx.Interaction.Member.Permissions&(discordgo.PermissionManageServer|discordgo.PermissionAdministrator) == 0 {
		return ui.Immediate(ui.Error("You need Manage Server to preview designs."))
	}
	styles := []string{"conversation"}
	data := ctx.Interaction.ApplicationCommandData()
	if option := data.GetOption("style"); option != nil && option.StringValue() == "all" {
		styles = []string{"compact", "conversation", "spotlight"}
	}
	if option := data.GetOption("style"); option != nil && option.StringValue() != "all" {
		selected := option.StringValue()
		if selected != "compact" && selected != "conversation" && selected != "spotlight" {
			return ui.Immediate(ui.Error("Choose Compact, Conversation, or Spotlight."))
		}
		styles = []string{selected}
	}
	section := "all"
	if option := data.GetOption("section"); option != nil {
		section = option.StringValue()
	}
	if !strings.Contains("|all|cases|dms|appeals|tickets|activity|icons|", "|"+section+"|") {
		return ui.Immediate(ui.Error("Choose a section from the preview menu."))
	}
	user := ctx.Interaction.Member.User
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		// Application emojis belong to their bot: Beta Bot must not render Quack's IDs.
		catalog, err := ctx.Session.ApplicationEmojis(ctx.Interaction.AppID, discordgo.WithContext(taskCtx))
		if err != nil {
			_, editErr := responder.EditOriginal(ui.EditMessage(ui.Content("I couldn’t load this bot’s custom icons. Try the preview again in a moment.", true)))
			return editErr
		}
		icons, missing := previewIconCatalog(catalog)
		if len(missing) != 0 {
			_, editErr := responder.EditOriginal(ui.EditMessage(ui.Content("This bot still needs these icons uploaded: `"+strings.Join(missing, "`, `")+"`.", true)))
			return editErr
		}
		send := func(message ui.Message) error {
			_, err := ctx.Session.ChannelMessageSendComplex(ctx.Interaction.ChannelID, message.SendParams(ctx.Interaction.AppID), discordgo.WithContext(taskCtx))
			return err
		}
		if section == "icons" {
			if err := send(previewIconGallery(icons)); err != nil {
				return err
			}
		} else {
			var samples []previewSample
			for _, sample := range uiPreviewSamples(user, time.Now()) {
				if section == "all" || sample.group == section {
					samples = append(samples, sample)
				}
			}
			for _, style := range styles {
				intro := "## " + icons["duck"] + " " + previewStyleName(style) + "\n" + previewStyleDescription(style) + "\n-# Design samples only · Buttons are disabled · DM examples appear here."
				if err := send(ui.Content(intro, false)); err != nil {
					return err
				}
				for i, sample := range samples {
					if err := taskCtx.Err(); err != nil {
						return err
					}
					message := previewDesign(sample, style, icons)
					message.Content += fmt.Sprintf("\n-# %s · %02d/%02d · %s", previewStyleName(style), i+1, len(samples), sample.name)
					if len([]rune(message.Content)) > 2000 {
						return fmt.Errorf("preview %s exceeds Discord's content limit", sample.name)
					}
					if err := send(message); err != nil {
						return fmt.Errorf("preview %s / %s: %w", style, sample.name, err)
					}
				}
			}
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Content("Your previews are ready. Use `/ui-preview style: section:` to compare a smaller group.", true)))
		return err
	})
}

// previewIconCatalog resolves names from the authenticated bot's own catalog.
func previewIconCatalog(catalog []*discordgo.Emoji) (map[string]string, []string) {
	icons := make(map[string]string)
	for _, emoji := range catalog {
		if emoji != nil && emoji.ID != "" && strings.HasPrefix(emoji.Name, "quack_") {
			icons[strings.TrimPrefix(emoji.Name, "quack_")] = emoji.MessageFormat()
		}
	}
	var missing []string
	for _, key := range previewIconKeys {
		if icons[key] == "" {
			missing = append(missing, "quack_"+key)
		}
	}
	return icons, missing
}

// previewStyleName gives each draft a stable name for design feedback.
func previewStyleName(style string) string {
	switch style {
	case "conversation":
		return "B · Conversation"
	case "spotlight":
		return "C · Spotlight"
	default:
		return "A · Compact"
	}
}

// previewStyleDescription explains the intended density before a gallery begins.
func previewStyleDescription(style string) string {
	switch style {
	case "conversation":
		return "Natural sentences, a little breathing room, and quoted context."
	case "spotlight":
		return "A short headline for the outcome, followed by the human details."
	default:
		return "The outcome first. One supporting line, with references kept small."
	}
}

// previewDesign renders hand-written copy into three text hierarchies. Context
// quotes are never rendered as field lists, and every control stays inert.
func previewDesign(sample previewSample, style string, icons map[string]string) ui.Message {
	icon := icons[sample.icon]
	var parts []string
	switch style {
	case "conversation":
		parts = append(parts, icon+" "+sample.lead)
		if sample.quote != "" {
			parts = append(parts, "> "+strings.ReplaceAll(sample.quote, "\n", "\n> "))
		}
		if sample.detail != "" {
			parts = append(parts, sample.detail)
		}
	case "spotlight":
		parts = append(parts, "### "+icon+" "+sample.title, sample.lead)
		if sample.detail != "" {
			parts = append(parts, sample.detail)
		}
		if sample.quote != "" {
			parts = append(parts, "> "+strings.ReplaceAll(sample.quote, "\n", "\n> "))
		}
	default:
		lead := icon + " " + sample.lead
		if sample.detail != "" {
			lead += "\n" + sample.detail
		}
		parts = append(parts, lead)
		if sample.quote != "" {
			parts = append(parts, "-# "+strings.ReplaceAll(sample.quote, "\n", "\n-# "))
		}
	}
	content := strings.Join(parts, "\n\n")
	if sample.meta != "" {
		content += "\n-# " + sample.meta
	}
	if style == "conversation" {
		content = strings.ReplaceAll(discordtext.Conversation(sample.icon, sample.lead, sample.quote, sample.detail, sample.meta), discordtext.Icon(sample.icon), icon)
	}
	message := ui.Message{Content: content, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if len(sample.buttons) != 0 {
		var buttons []discordgo.MessageComponent
		for i, label := range sample.buttons {
			buttons = append(buttons, ui.Button(fmt.Sprintf("preview:disabled:v1:%d", i), label, discordgo.SecondaryButton, true))
		}
		message.Components = []discordgo.MessageComponent{ui.Row(buttons...)}
	}
	return message
}

// previewIconGallery allows small-size inspection of every uploaded application icon.
func previewIconGallery(icons map[string]string) ui.Message {
	lines := []string{"### " + icons["duck"] + " Quack icons"}
	for _, key := range previewIconKeys {
		lines = append(lines, icons[key]+" `"+key+"`")
	}
	return ui.Message{Content: strings.Join(lines, "\n"), AllowedMentions: &discordgo.MessageAllowedMentions{}}
}
