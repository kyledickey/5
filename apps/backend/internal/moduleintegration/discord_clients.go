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

// ticketDiscordClient implements the module's narrow private-channel port with
// Discord resources resolved from the integration-owned guild adapter.
type ticketDiscordClient struct {
	session  *discordgo.Session
	resolver guildResolver
}

// CreatePrivateTicketChannel opens a private, non-invitable thread under the entry channel.
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
	if entryChannel == nil || entryChannel.GuildID != discordGuildID || entryChannel.Type != discordgo.ChannelTypeGuildText {
		return "", errors.New("ticket entry must be a text channel in this guild")
	}
	name := "ticket-" + ticketNameSuffix(ownerID)
	thread, err := c.session.ThreadStartComplex(settings.EntryChannelDiscordID, &discordgo.ThreadStart{
		Name: name, Type: discordgo.ChannelTypeGuildPrivateThread,
		AutoArchiveDuration: 1440, Invitable: false,
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", err
	}
	return thread.ID, nil
}

// EnsureTicketPermissions invites the owner into a verified private thread.
// Moderators join on demand through the queue instead of receiving mass invitations.
func (c ticketDiscordClient) EnsureTicketPermissions(ctx context.Context, channelID, ownerID, guildID string) error {
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel == nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildPrivateThread {
		return errors.New("ticket must be a private thread in this guild")
	}
	if err := c.session.ThreadMemberAdd(channelID, ownerID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
		return err
	}
	return c.syncTicketThreadMembers(ctx, discordGuildID, channelID, ownerID)
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
		for _, message := range page {
			if message == nil || message.ID == "" {
				return "", errors.New("Discord returned an incomplete ticket history page")
			}
		}
		messages = append(messages, page...)
		if len(page) < 100 {
			break
		}
		next := page[len(page)-1].ID
		if before != "" && (len(next) > len(before) || len(next) == len(before) && next >= before) {
			return "", errors.New("Discord ticket history pagination did not advance")
		}
		before = next
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Timestamp.Equal(messages[j].Timestamp) {
			if len(messages[i].ID) != len(messages[j].ID) {
				return len(messages[i].ID) < len(messages[j].ID)
			}
			return messages[i].ID < messages[j].ID
		}
		return messages[i].Timestamp.Before(messages[j].Timestamp)
	})
	var transcript strings.Builder
	seen := make(map[string]struct{}, len(messages))
	for _, message := range messages {
		if _, exists := seen[message.ID]; exists {
			continue
		}
		seen[message.ID] = struct{}{}
		authorID := "unknown"
		if message.Author != nil {
			authorID = message.Author.Username + " (" + message.Author.ID + ")"
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n", message.Timestamp.UTC().Format(time.RFC3339), authorID, message.Content)
		for _, attachment := range message.Attachments {
			if attachment != nil {
				fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Filename, attachment.Size)
				if attachment.URL != "" {
					fmt.Fprintf(&transcript, "  original attachment URL (may expire): %s\n", attachment.URL)
				}
			}
		}
	}
	return transcript.String(), nil
}

// FreezeTicketChannel closes normal thread posting before transcript capture.
// Retrying the lock is safe if a previous close stopped before publication.
func (c ticketDiscordClient) FreezeTicketChannel(ctx context.Context, channelID string) error {
	archived, locked := true, true
	_, err := c.session.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Archived: &archived, Locked: &locked}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// DeleteTicketChannel removes a conversation only after its transcript receipt
// was persisted by the ticket adapter. Missing channels make retry idempotent.
func (c ticketDiscordClient) DeleteTicketChannel(ctx context.Context, channelID string) error {
	_, err := c.session.ChannelDelete(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownChannel {
		return nil
	}
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

// SendTicketWelcome invites the owner to begin the conversation without an intake form.
// Only the ticket owner may be mentioned by this bot-authored message.
func (c ticketDiscordClient) SendTicketWelcome(ctx context.Context, ticket *tickets.Ticket) error {
	message := ui.Signal("ticket", fmt.Sprintf("<@%s> A mod will be here soon. Feel free to tell us what's up while you wait.", ticket.OwnerDiscordUserID), false)
	message.Components = tickets.TicketComponents(ticket.ID)
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	payload.AllowedMentions = &discordgo.MessageAllowedMentions{Users: []string{ticket.OwnerDiscordUserID}}
	_, err := c.session.ChannelMessageSendComplex(ticket.ThreadDiscordChannelID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// JoinTicketThread adds the moderator authorized by the ticket adapter.
func (c ticketDiscordClient) JoinTicketThread(ctx context.Context, channelID, userID string) error {
	return c.session.ThreadMemberAdd(channelID, userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
}
