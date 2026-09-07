package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

const ticketChannelPermissions = discordgo.PermissionViewChannel |
	discordgo.PermissionSendMessages |
	discordgo.PermissionReadMessageHistory |
	discordgo.PermissionAttachFiles |
	discordgo.PermissionEmbedLinks

// ticketDiscordClient implements the module's narrow private-channel port with
// Discord resources resolved from the integration-owned guild adapter.
type ticketDiscordClient struct {
	session  *discordgo.Session
	resolver guildResolver
}

// CreatePrivateTicketChannel provisions the configured private thread or text channel.
func (c ticketDiscordClient) CreatePrivateTicketChannel(ctx context.Context, guildID, ownerID string, settings tickets.Settings) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return "", err
	}
	entryChannel, err := c.session.Channel(settings.EntryChannelDiscordID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", err
	}
	if entryChannel.GuildID != discordGuildID {
		return "", errors.New("ticket entry channel belongs to another guild")
	}
	name := "ticket-" + ticketNameSuffix(ownerID)
	if settings.UsePrivateThreads {
		thread, err := c.session.ThreadStartComplex(settings.EntryChannelDiscordID, &discordgo.ThreadStart{
			Name: name, Type: discordgo.ChannelTypeGuildPrivateThread,
			AutoArchiveDuration: 1440, Invitable: false,
		}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return "", err
		}
		// The adapter validates owner and staff membership before committing the ticket.
		return thread.ID, nil
	}

	botID, err := c.botUserID(ctx)
	if err != nil {
		return "", err
	}
	overwrites := ticketPermissionOverwrites(discordGuildID, ownerID, botID, settings.StaffRoleDiscordIDs)
	channel, err := c.session.GuildChannelCreateComplex(discordGuildID, discordgo.GuildChannelCreateData{
		Name: name, Type: discordgo.ChannelTypeGuildText,
		ParentID: entryChannel.ParentID, PermissionOverwrites: overwrites,
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", err
	}
	return channel.ID, nil
}

// EnsureTicketPermissions validates private-thread inheritance or replaces a
// text channel's ACL with owner, configured staff, and bot-only visibility.
func (c ticketDiscordClient) EnsureTicketPermissions(ctx context.Context, channelID, ownerID, guildID string, staffRoleIDs []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel.GuildID != discordGuildID {
		return errors.New("ticket channel belongs to another guild")
	}
	if channel.IsThread() {
		if channel.Type != discordgo.ChannelTypeGuildPrivateThread {
			return errors.New("ticket thread is not private")
		}
		if err := c.session.ThreadMemberAdd(channelID, ownerID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
			return err
		}
		return c.syncTicketThreadMembers(ctx, discordGuildID, channelID, ownerID, staffRoleIDs)
	}
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	updated, err := c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{
		PermissionOverwrites: ticketPermissionOverwrites(discordGuildID, ownerID, botID, staffRoleIDs),
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	return validateTicketACL(updated, discordGuildID, ownerID, botID, staffRoleIDs)
}

// botUserID returns the current application identity needed for explicit ACLs.
func (c ticketDiscordClient) botUserID(ctx context.Context) (string, error) {
	if c.session.State != nil && c.session.State.User != nil && c.session.State.User.ID != "" {
		return c.session.State.User.ID, nil
	}
	user, err := c.session.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	if user == nil || user.ID == "" {
		return "", errors.New("Discord bot identity is unavailable")
	}
	return user.ID, nil
}

// DeleteProvisionalTicketChannel removes only a freshly provisioned resource
// whose permissions failed before its ticket was committed.
func (c ticketDiscordClient) DeleteProvisionalTicketChannel(ctx context.Context, channelID string) error {
	_, err := c.session.ChannelDelete(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// SendTicketReply sends one mention-suppressed message inside the private ticket.
func (c ticketDiscordClient) SendTicketReply(ctx context.Context, channelID, body string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSendComplex(channelID, ui.Conversation("reply", "A reply to your ticket.", ui.PlainText(body), "", "", false).SendParams(ui.SessionApplicationID(c.session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// CaptureTicketTranscript snapshots the complete available private message
// history in stable chronological order before closure.
func (c ticketDiscordClient) CaptureTicketTranscript(ctx context.Context, channelID string) (string, error) {
	before := ""
	messages := make([]*discordgo.Message, 0, 100)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		page, err := c.session.ChannelMessages(channelID, 100, before, "", "", discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return "", err
		}
		messages = append(messages, page...)
		if len(page) < 100 {
			break
		}
		before = page[len(page)-1].ID
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].Timestamp.Before(messages[j].Timestamp) })
	var transcript strings.Builder
	for _, message := range messages {
		authorID := "unknown"
		if message.Author != nil {
			authorID = message.Author.ID
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n", message.Timestamp.UTC().Format(time.RFC3339), authorID, message.Content)
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Filename, attachment.Size)
		}
	}
	return transcript.String(), nil
}

// ArchiveTicketChannel archives a thread or deletes a dedicated private text
// channel after its transcript has been durably captured.
func (c ticketDiscordClient) ArchiveTicketChannel(ctx context.Context, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel.IsThread() {
		archived := true
		locked := true
		_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		return err
	}
	overwrites := make([]*discordgo.PermissionOverwrite, 0, len(channel.PermissionOverwrites))
	for _, existing := range channel.PermissionOverwrites {
		copy := *existing
		if copy.ID != channel.GuildID {
			copy.Allow &^= discordgo.PermissionSendMessages
			copy.Deny |= discordgo.PermissionSendMessages
		}
		overwrites = append(overwrites, &copy)
	}
	name := channel.Name
	if !strings.HasPrefix(name, "closed-") {
		name = "closed-" + name
	}
	_, err = c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Name: name, PermissionOverwrites: overwrites}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// ticketNameSuffix keeps Discord channel names bounded and non-sensitive.
func ticketNameSuffix(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if len(ownerID) > 12 {
		ownerID = ownerID[len(ownerID)-12:]
	}
	if ownerID == "" {
		return "member"
	}
	return ownerID
}
