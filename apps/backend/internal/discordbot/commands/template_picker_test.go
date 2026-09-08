package commands

import (
	"fmt"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestTemplatePickerReachesEveryTemplateAndPreservesTarget(t *testing.T) {
	templates := make([]quack.TemplateResponse, 51)
	for i := range templates {
		templates[i] = quack.TemplateResponse{ID: fmt.Sprintf("template-%d", i), Name: fmt.Sprintf("Rule %d", i)}
	}
	for _, target := range []struct{ kind, payload, action string }{
		{"u", "489264179472236557", "user_template"},
		{"m", "489264179472236557|1005778938108325970|1005778938108325971", "message_template"},
	} {
		t.Run(target.kind, func(t *testing.T) {
			seen := map[string]bool{}
			for page := 0; page < 3; page++ {
				message := caseTemplatePicker(templates, target.kind, target.payload, page)
				menu := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
				id, err := ui.DecodeCustomID(menu.CustomID)
				if err != nil || id.Payload != target.payload || id.Action != target.action {
					t.Fatalf("selection lost target: %+v, %v", id, err)
				}
				for _, option := range menu.Options {
					if seen[option.Value] {
						t.Fatalf("duplicate template %s", option.Value)
					}
					seen[option.Value] = true
				}
				buttons := message.Components[1].(discordgo.ActionsRow).Components
				for i, component := range buttons {
					button := component.(discordgo.Button)
					next := max(0, page-1)
					if i == 1 {
						next = page + 1
					}
					id, err := ui.DecodeCustomID(button.CustomID)
					if err != nil || id.Payload != fmt.Sprintf("%s|%d|%s", target.kind, next, target.payload) || id.Action != "template_page" {
						t.Fatalf("navigation lost target: %+v, %v", id, err)
					}
					if button.Disabled != ((i == 0 && page == 0) || (i == 1 && page == 2)) {
						t.Fatal("incorrect boundary button")
					}
				}
			}
			if len(seen) != len(templates) {
				t.Fatalf("reached %d of %d templates", len(seen), len(templates))
			}
		})
	}
}

func TestTemplatePickerClampsAfterTemplatesAreRemoved(t *testing.T) {
	message := caseTemplatePicker([]quack.TemplateResponse{{ID: "remaining", Name: "Remaining"}}, "u", "target", 99)
	menu := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if len(message.Components) != 1 || len(menu.Options) != 1 || menu.Options[0].Value != "remaining" {
		t.Fatal("stale page did not reach remaining template")
	}
	if empty := caseTemplatePicker(nil, "u", "target", 99); len(empty.Components) != 0 {
		t.Fatal("empty list rendered a select menu")
	}
}
