package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestEvidenceChannelPreservesAdministratorChanges ensures routine startup and
// channel events cannot undo a server's chosen storage name or permissions.
func TestEvidenceChannelPreservesAdministratorChanges(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	calls := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/channels/storage") {
			t.Fatalf("existing channel was modified: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"storage","guild_id":"guild","type":0,"name":"our-evidence","parent_id":"staff-category","permission_overwrites":[]}`)), Request: request}, nil
	})}
	id, err := (&Bot{Session: session}).EnsureEvidenceChannel(context.Background(), "guild", "storage")
	if err != nil || id != "storage" || calls != 1 {
		t.Fatalf("existing storage not reused: id=%s calls=%d err=%v", id, calls, err)
	}
}

// TestEvidenceCopyReturnsReopenableMessageLink verifies that administrator-chosen
// channel visibility does not prevent storage and that receipts link to saved messages.
func TestEvidenceCopyReturnsReopenableMessageLink(t *testing.T) {
	for _, advertisedSize := range []int64{2, 3, 4} {
		t.Run(fmt.Sprintf("metadata_%d_download_3", advertisedSize), func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			uploads := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				body := `{"id":"storage","guild_id":"guild","type":0,"permission_overwrites":[]}`
				if request.Method == http.MethodPost {
					uploads++
					reader, err := request.MultipartReader()
					if err != nil {
						t.Fatal(err)
					}
					foundFile := false
					for {
						part, err := reader.NextPart()
						if errors.Is(err, io.EOF) {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						if part.FileName() != "" {
							content, err := io.ReadAll(part)
							if err != nil || string(content) != "abc" || part.FileName() != "proof.png" {
								t.Fatalf("downloaded representation changed: %q %v", content, err)
							}
							foundFile = true
						}
					}
					if !foundFile {
						t.Fatal("copy omitted the attachment")
					}
					body = `{"id":"copy","channel_id":"storage","attachments":[{"id":"file","url":"https://cdn.discordapp.com/attachments/storage/file/proof.png?expires=soon"}]}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}
			client := &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("abc")), Request: request}, nil
			})}
			saved, err := (&Bot{Session: session, HTTPClient: client}).PreserveEvidenceAttachment(context.Background(), "guild", "storage", quack.DiscordAttachmentSnapshot{Filename: "proof.png", SizeBytes: advertisedSize, URL: "https://cdn.discordapp.com/attachments/source/file/proof.png"})
			if err != nil || uploads != 1 || saved.URL != "https://discord.com/channels/guild/storage/copy" {
				t.Fatalf("copy receipt was not a stable message link: saved=%+v uploads=%d err=%v", saved, uploads, err)
			}
		})
	}

}

// TestCreatedEvidenceChannelAllowsCurrentStaffOnly covers fresh creation and
// deleted-channel repair with effective Discord permission evaluation. Stale
// cached roles must not grant evidence access to a former moderation role.
func TestCreatedEvidenceChannelAllowsCurrentStaffOnly(t *testing.T) {
	for _, current := range []string{"", "deleted"} {
		t.Run("previous_"+current, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.State.User = &discordgo.User{ID: "bot"}
			stale := &discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{{ID: "former-mod", Permissions: discordgo.PermissionModerateMembers}}}
			if err := session.State.GuildAdd(stale); err != nil {
				t.Fatal(err)
			}
			roles := []*discordgo.Role{
				{ID: "guild", Permissions: discordgo.PermissionViewChannel},
				{ID: "mod", Permissions: discordgo.PermissionModerateMembers},
				{ID: "manager", Permissions: discordgo.PermissionManageGuild},
				{ID: "former-mod", Permissions: discordgo.PermissionViewChannel},
			}
			var created discordgo.GuildChannelCreateData
			roleLookups, introductions := 0, 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				status, body := http.StatusOK, "{}"
				switch {
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/channels/deleted"):
					status, body = http.StatusNotFound, `{"code":10003,"message":"Unknown Channel"}`
				case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/guilds/guild/roles"):
					roleLookups++
					encoded, _ := json.Marshal(roles)
					body = string(encoded)
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/guilds/guild/channels"):
					if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
						t.Fatal(err)
					}
					body = `{"id":"new-storage","guild_id":"guild","type":0}`
				case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/channels/new-storage/messages"):
					introductions++
					var message discordgo.MessageSend
					if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(message.Content, "# Case evidence\n") || message.AllowedMentions == nil {
						t.Fatalf("wrong intro: %+v", message)
					}
					body = `{"id":"intro"}`
				default:
					t.Fatalf("unexpected Discord call: %s %s", request.Method, request.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}
			id, err := (&Bot{Session: session}).EnsureEvidenceChannel(context.Background(), "guild", current)
			if err != nil || id != "new-storage" || roleLookups != 1 || introductions != 1 || created.Topic == "" {
				t.Fatalf("creation failed: id=%s lookups=%d err=%v", id, roleLookups, err)
			}
			guild := &discordgo.Guild{ID: "guild", Roles: roles, Members: []*discordgo.Member{
				{User: &discordgo.User{ID: "member"}},
				{User: &discordgo.User{ID: "moderator"}, Roles: []string{"mod"}},
				{User: &discordgo.User{ID: "administrator"}, Roles: []string{"manager"}},
				{User: &discordgo.User{ID: "former"}, Roles: []string{"former-mod"}},
				{User: &discordgo.User{ID: "bot"}},
			}, Channels: []*discordgo.Channel{{ID: id, GuildID: "guild", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: created.PermissionOverwrites}}}
			state := discordgo.NewState()
			if err := state.GuildAdd(guild); err != nil {
				t.Fatal(err)
			}
			read := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
			for _, user := range []string{"member", "former", "moderator", "administrator", "bot"} {
				permissions, err := state.UserChannelPermissions(user, id)
				if err != nil {
					t.Fatal(err)
				}
				if user == "member" || user == "former" {
					if permissions&discordgo.PermissionViewChannel != 0 {
						t.Fatalf("nonstaff %s can view evidence", user)
					}
				} else if permissions&read != read {
					t.Fatalf("%s cannot open preserved evidence", user)
				}
				if user == "bot" && permissions&(discordgo.PermissionSendMessages|discordgo.PermissionAttachFiles) != discordgo.PermissionSendMessages|discordgo.PermissionAttachFiles {
					t.Fatal("bot cannot preserve files")
				}
			}
		})
	}
}

// TestEvidenceCopyRejectsUnreadableAndOversizedDownloads keeps actual bytes
// bounded even when Discord advertises a small file; failures never upload.
func TestEvidenceCopyRejectsUnreadableAndOversizedDownloads(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		size      int64
		readError error
		want      string
	}{
		{name: "oversized", size: quack.MaxPreservedAttachmentBytes + 128, want: "exceeds the managed copy size limit"},
		{name: "read failure", size: 2, readError: errors.New("private transport failure"), want: "could not be read"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			body := &evidenceTestDownload{remaining: scenario.size, readError: scenario.readError}
			client := &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(body), Request: request}, nil
			})}
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				t.Fatalf("failed download reached Discord: %s", request.URL.Path)
				return nil, errors.New("unexpected Discord request")
			})}
			saved, err := (&Bot{Session: session, HTTPClient: client}).PreserveEvidenceAttachment(context.Background(), "guild", "storage", quack.DiscordAttachmentSnapshot{Filename: "proof.png", SizeBytes: 1, URL: "https://cdn.discordapp.com/attachments/source/file/proof.png"})
			if saved != nil || err == nil || !strings.Contains(err.Error(), scenario.want) || strings.Contains(err.Error(), "private") {
				t.Fatalf("invalid download was accepted or misreported: saved=%+v err=%v", saved, err)
			}
			if scenario.readError == nil && body.read != quack.MaxPreservedAttachmentBytes+1 {
				t.Fatalf("download read %d bytes; expected hard cap plus one", body.read)
			}
		})
	}
}

// evidenceTestDownload generates a bounded stream without allocating a large
// source fixture and can fail after delivering a partial response body.
type evidenceTestDownload struct {
	remaining int64
	read      int64
	readError error
}

// Read supplies zero bytes until exhausted, then reports the configured failure.
func (r *evidenceTestDownload) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		if r.readError != nil {
			return 0, r.readError
		}
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.remaining))
	clear(p[:n])
	r.remaining -= int64(n)
	r.read += int64(n)
	return n, nil
}
