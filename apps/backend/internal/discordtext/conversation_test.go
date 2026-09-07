package discordtext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestUploadedCatalogMatchesSharedAssets prevents cross-application emoji IDs
// and stale backend references when the approved shared icon manifest changes.
func TestUploadedCatalogMatchesSharedAssets(t *testing.T) {
	data, err := os.ReadFile("../../../../assets/icons/quack/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Icons []struct {
			Key, Name string
			EmojiIDs  map[string]string `json:"emojiIds"`
		}
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, icon := range manifest.Icons {
		for appID, id := range icon.EmojiIDs {
			want := "<:" + icon.Name + ":" + id + ">"
			if got := Resolve(Icon(icon.Key), appID); got != want {
				t.Errorf("%s/%s: got %q want %q", appID, icon.Key, got, want)
			}
		}
	}
	for appID, icons := range applicationIcons {
		if len(icons) != len(manifest.Icons) {
			t.Errorf("%s catalog is incomplete", appID)
		}
	}
}

// TestUntrustedContextStaysQuoted checks multi-line Markdown and marker escaping.
func TestUntrustedContextStaysQuoted(t *testing.T) {
	body := Conversation("case", "Case added.", Plain("**reason**\n{{quack:ban}}\n> extra"), "You can appeal.", "Case #12")
	got := Resolve(body, "819019613371236432")
	if strings.Contains(got, "<:quack_ban:") || !strings.Contains(got, "> \\*\\*reason\\*\\*\n> ") || !strings.HasSuffix(got, "\n-# Case #12") {
		t.Fatal(got)
	}
	if Resolve(Icon("case")+" Case added.", "unknown") != "Case added." {
		t.Fatal("unknown application lost readable fallback")
	}
}
