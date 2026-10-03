package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

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

// CaptureTicketMessages snapshots available private history with stable message
// identities, allowing closure to merge retained original text after deletions.
func (c ticketDiscordClient) CaptureTicketMessages(ctx context.Context, channelID string) ([]tickets.TranscriptMessage, error) {
	before := ""
	messages := make([]*discordgo.Message, 0, 100)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := c.session.ChannelMessages(channelID, 100, before, "", "", discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return nil, err
		}
		for _, message := range page {
			if message == nil || message.ID == "" {
				return nil, errors.New("Discord returned an incomplete ticket history page")
			}
		}
		messages = append(messages, page...)
		if len(page) < 100 {
			break
		}
		next := page[len(page)-1].ID
		if before != "" && (len(next) > len(before) || len(next) == len(before) && next >= before) {
			return nil, errors.New("Discord ticket history pagination did not advance")
		}
		before = next
	}
	result := make([]tickets.TranscriptMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, ticketTranscriptMessage(message))
	}
	return result, nil
}

// CaptureTicketTranscript retains the older text-only client port for callers
// outside native journal closure; both paths share the same checked pagination.
func (c ticketDiscordClient) CaptureTicketTranscript(ctx context.Context, channelID string) (string, error) {
	messages, err := c.CaptureTicketMessages(ctx, channelID)
	if err != nil {
		return "", err
	}
	return tickets.FormatTranscript(messages), nil
}

func ticketTranscriptMessage(message *discordgo.Message) tickets.TranscriptMessage {
	snapshot := tickets.TranscriptMessage{MessageID: message.ID, Body: message.Content, SentAt: message.Timestamp}
	if message.Author != nil {
		snapshot.AuthorID = message.Author.ID
		snapshot.AuthorName = message.Author.Username
	}
	for _, attachment := range message.Attachments {
		if attachment != nil {
			snapshot.Attachments = append(snapshot.Attachments, tickets.TranscriptAttachment{Name: attachment.Filename, Size: attachment.Size, URL: attachment.URL})
		}
	}
	return snapshot
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
	message.Components = ticketCloseComponents(ticket.ID)
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	payload.AllowedMentions = &discordgo.MessageAllowedMentions{Users: []string{ticket.OwnerDiscordUserID}}
	_, err := c.session.ChannelMessageSendComplex(ticket.ThreadDiscordChannelID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

func (c ticketDiscordClient) JoinTicketThread(ctx context.Context, channelID, userID string) error {
	return c.session.ThreadMemberAdd(channelID, userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
}

// syncTicketThreadMembers removes invitations held by former moderators.
// It inspects only existing thread members, never the entire guild, and does not
// invite staff who have not chosen to join the conversation.
func (c ticketDiscordClient) syncTicketThreadMembers(ctx context.Context, guildID, threadID, ownerID string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	guild, err := c.session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if guild == nil {
		return errors.New("guild authorization unavailable")
	}
	roles := make(map[string]int64)
	for _, role := range guild.Roles {
		if role != nil {
			roles[role.ID] = role.Permissions
		}
	}
	after := ""
	for {
		members, err := c.session.ThreadMembers(threadID, 100, false, after, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return err
		}
		for _, member := range members {
			if member == nil || member.UserID == "" {
				return errors.New("Discord returned an invalid thread member")
			}
			if member.UserID == ownerID || member.UserID == botID || member.UserID == guild.OwnerID {
				continue
			}
			current, err := c.session.GuildMember(guildID, member.UserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
			var rest *discordgo.RESTError
			if err != nil && !(errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMember) {
				return err
			}
			permissions := roles[guildID]
			if current != nil {
				for _, id := range current.Roles {
					permissions |= roles[id]
				}
			}
			if current != nil && permissions&(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) != 0 {
				continue
			}
			if err := c.session.ThreadMemberRemove(threadID, member.UserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
				return err
			}
		}
		if len(members) < 100 {
			break
		}
		next := members[len(members)-1].UserID
		if next == after {
			return errors.New("Discord repeated a thread-member page")
		}
		after = next
	}
	return nil
}

// publishTicketEntry edits the existing panel when setup keeps the same channel.
// Moving the entry first retires the old panel. A failed retirement keeps its
// saved reference available for retry and prevents another active panel appearing.
// Only an explicit missing-message response permits falling back to a new send.
func (c ticketDiscordClient) publishTicketEntry(ctx context.Context, settings tickets.Settings) (*discordgo.Message, error) {
	if settings.EntryPanelMessageID != "" && settings.EntryPanelChannelID != "" && settings.EntryPanelChannelID != settings.EntryChannelDiscordID {
		if err := c.retireTicketEntry(ctx, settings); err != nil {
			return nil, err
		}
	}
	message := ui.Message{Content: "# Need a hand?\nTalk privately with the mod team. Open a ticket and tell us what’s going on."}
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "open", Version: "v1"}), "Open ticket", discordgo.PrimaryButton, false))}
	payload := message.SendParams(ui.SessionApplicationID(c.session))
	var sent *discordgo.Message
	var err error
	if settings.EntryPanelMessageID != "" && settings.EntryPanelChannelID == settings.EntryChannelDiscordID {
		sent, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: settings.EntryPanelMessageID, Channel: settings.EntryChannelDiscordID, Content: &payload.Content, Components: &payload.Components, AllowedMentions: payload.AllowedMentions}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		var rest *discordgo.RESTError
		if err != nil && !(errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage) {
			return nil, err
		}
	}
	if sent == nil {
		sent, err = c.session.ChannelMessageSendComplex(settings.EntryChannelDiscordID, payload, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	}
	if err != nil {
		return nil, err
	}
	if sent == nil || sent.ID == "" {
		return nil, errors.New("Discord did not return the entry panel message")
	}
	return sent, nil
}

// retireTicketEntry replaces a moved panel with a pointer to the new entry and
// removes all controls. It is safe to repeat after a partial setup failure. A
// deleted message or channel already satisfies retirement; permission and network
// failures remain visible so setup cannot lose the old panel's recovery reference.
func (c ticketDiscordClient) retireTicketEntry(ctx context.Context, settings tickets.Settings) error {
	content := fmt.Sprintf("Tickets have moved to <#%s>. Open a ticket there.", settings.EntryChannelDiscordID)
	components := []discordgo.MessageComponent{}
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: settings.EntryPanelMessageID, Channel: settings.EntryPanelChannelID,
		Content: &content, Components: &components, AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && (rest.Message.Code == discordgo.ErrCodeUnknownMessage || rest.Message.Code == discordgo.ErrCodeUnknownChannel) {
		return nil
	}
	return err
}

type ticketPermission struct {
	bit  int64
	name string
}

// validateTicketBotPermissions evaluates fresh REST state rather than gateway
// caches. Setup requires the capabilities used by opening, closure and transcripts.
func (c ticketDiscordClient) validateTicketBotPermissions(ctx context.Context, guildID, entryID, queueID string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	guild, err := c.session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || guild == nil {
		return errors.New("could not check Quack's current server permissions")
	}
	member, err := c.session.GuildMember(guildID, botID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || member == nil {
		return errors.New("could not check Quack's current server membership")
	}
	common := []ticketPermission{{discordgo.PermissionViewChannel, "View Channel"}, {discordgo.PermissionSendMessages, "Send Messages"}, {discordgo.PermissionReadMessageHistory, "Read Message History"}}
	for _, destination := range []struct {
		id       string
		required []ticketPermission
	}{
		{entryID, append(append([]ticketPermission{}, common...), ticketPermission{discordgo.PermissionCreatePrivateThreads, "Create Private Threads"}, ticketPermission{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"}, ticketPermission{discordgo.PermissionManageThreads, "Manage Threads"})},
		{queueID, append(append([]ticketPermission{}, common...), ticketPermission{discordgo.PermissionAttachFiles, "Attach Files"})},
	} {
		channel, err := c.session.Channel(destination.id, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || channel == nil || channel.GuildID != guildID || channel.Type != discordgo.ChannelTypeGuildText {
			return errors.New("ticket destinations must be text channels in this server")
		}
		snapshot := *guild
		snapshot.Channels = []*discordgo.Channel{channel}
		snapshot.Members = []*discordgo.Member{member}
		state := discordgo.NewState()
		if err := state.GuildAdd(&snapshot); err != nil {
			return err
		}
		permissions, err := state.UserChannelPermissions(botID, channel.ID)
		if err != nil {
			return errors.New("could not calculate Quack's channel permissions")
		}
		var missing []string
		for _, required := range destination.required {
			if permissions&required.bit == 0 {
				missing = append(missing, required.name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("Quack needs %s in <#%s>. Update the channel permissions and run setup again", strings.Join(missing, ", "), channel.ID)
		}
	}
	return nil
}

var _ tickets.DiscordClient = ticketDiscordClient{}
