package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// CommandSpec binds one Discord command definition to the handler that implements it.
type CommandSpec struct {
	Definition *discordgo.ApplicationCommand
	Handler    ui.Handler
}

type Registry struct {
	commands map[string]CommandSpec
}

// ComponentRegistrar installs integration-owned component and modal handlers
// into the same dispatcher as application commands.
type ComponentRegistrar func(*interactions.ComponentRegistry) error

func NewRegistry() *Registry {
	return &Registry{commands: map[string]CommandSpec{}}
}

func (r *Registry) Register(spec CommandSpec) error {
	if r == nil {
		return errors.New("command registry is not configured")
	}
	if spec.Definition == nil {
		return errors.New("command definition is required")
	}

	name := strings.TrimSpace(spec.Definition.Name)
	if name == "" {
		return errors.New("command name is required")
	}
	if spec.Handler == nil {
		return fmt.Errorf("command %s handler is required", name)
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %s is already registered", name)
	}

	r.commands[name] = spec
	return nil
}

// Specs returns a copy of registered command specifications so callers cannot mutate registry state.
func (r *Registry) Specs() []CommandSpec {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]CommandSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, r.commands[name])
	}
	return specs
}

// LookupCommand satisfies interactions.CommandLookup.
func (r *Registry) LookupCommand(name string) (ui.Handler, bool) {
	if r == nil {
		return nil, false
	}
	spec, ok := r.commands[name]
	if !ok {
		return nil, false
	}
	return spec.Handler, true
}

// Infrastructure supplies adapter capabilities independently of application services.
// A nil CommandHashes disables command caching; a nil Deduper keeps the dispatcher's
// process-local replay protection. Callers retain ownership of these dependencies.
type Infrastructure struct {
	CommandHashes CommandHashStore
	Deduper       *interactions.InteractionDeduper
}

// Register installs native handlers and synchronizes definitions using explicitly supplied infrastructure.
func Register(session *discordgo.Session, services *quack.Services, infrastructure Infrastructure, moduleSetup SetupHandlers, componentRegistrars ...ComponentRegistrar) error {
	if session == nil {
		return errors.New("discord session is not configured")
	}
	if services == nil {
		return errors.New("app services are not configured")
	}

	registry := NewRegistry()
	specs := []CommandSpec{
		HelpCommandSpec(),
		TemplateCommandSpec(),
		AppealsCommandSpec(),
		SetupCommandSpec(moduleSetup),
		CaseCommandSpec(),
		UserCaseCommandSpec(),
		MessageCaseCommandSpec(),
	}
	// The temporary gallery is never registered on staging or production bots.
	if services.Config.Environment == "dev" {
		specs = append(specs, UIPreviewCommandSpec())
	}
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			return err
		}
	}
	dispatcher := interactions.NewDispatcher(services, registry)
	// Core controls are registered even when no optional modules are composed.
	for _, register := range []ComponentRegistrar{
		RegisterTemplateComponents,
		RegisterCaseComponents,
		RegisterAppealQueueComponents,
		func(registry *interactions.ComponentRegistry) error {
			return discordadapter.RegisterAppealComponents(registry, services, services.Appeals)
		},
	} {
		if err := register(dispatcher.Components); err != nil {
			return err
		}
	}
	if infrastructure.Deduper != nil {
		dispatcher.Deduper = infrastructure.Deduper
	}
	for _, register := range componentRegistrars {
		if register == nil {
			continue
		}
		if err := register(dispatcher.Components); err != nil {
			return fmt.Errorf("register Discord components: %w", err)
		}
	}
	session.AddHandler(dispatcher.Handle)

	appID := strings.TrimSpace(services.Config.Discord.AppID)
	if appID == "" && session.State != nil && session.State.User != nil {
		appID = session.State.User.ID
	}
	if appID == "" {
		return errors.New("discord app id is not configured")
	}

	syncer := CommandSyncer{
		Client:       sessionCommandClient{session: session},
		Cache:        newRedisCommandCache(infrastructure.CommandHashes),
		AppID:        appID,
		PruneEnabled: services.Config.Discord.CommandPrune,
		GuildID: strings.TrimSpace(
			services.Config.Discord.CommandGuildID,
		),
	}
	return syncer.Sync(context.Background(), registry.Specs())
}

// HelpCommandSpec keeps explanations out of everyday results while making each
// native workflow discoverable without leaving Discord.
func HelpCommandSpec() CommandSpec {
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Name: "help", Description: "Find your way around Quack", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionString, Name: "topic", Description: "What do you need help with?", Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Rules and repeat offenses", Value: "rules"}, {Name: "Cases and evidence", Value: "cases"}, {Name: "Appeals", Value: "appeals"}, {Name: "Tickets and setup", Value: "setup"}}}}}, Handler: func(ctx ui.Context) ui.HandlerResult {
		topic := ""
		if option := ctx.Interaction.ApplicationCommandData().GetOption("topic"); option != nil {
			topic = option.StringValue()
		}
		body := "## Hey, I’m Quack.\nI keep track of moderation cases, handle appeals, and help your staff run the server.\n\n**Moderation:** `/case add`, `/case list`, `/case user`\n**Rules:** `/template create`, `/template view`\n**Server setup:** `/setup tickets`, `/setup appeals`, `/setup honeypot`, `/setup logging`, `/setup audit`\n\nPick a topic in `/help` for a closer look."
		switch topic {
		case "rules":
			body = "## Rules and repeat offenses\nA rule pairs a reason with a punishment. Pick the rule when adding a case; I’ll choose its punishment.\n\nFor example: **first time → warning, second time → 5-minute timeout, third time → ban.** Counts are per member, per rule.\n\nStart with `/template create`. Add a step with `/template level`: `case:2 outcome:Timeout minutes:5`. The second case gets a timeout, and so do later cases until another step takes over.\n\n`/template remove-level` removes a step by its count. Removing step 2 leaves the first step in effect until the next one.\n\nUse `/template edit` to change the name, reason, appeals, or counting window. A seven-day window counts only cases from the last week. Older cases stay in history. Voided cases and imported v4 cases never raise the punishment. Changes apply to new cases.\n\n`/template view` shows the current rule. Archive a rule to stop using it without deleting its history."
		case "cases":
			body = "## Cases and evidence\nUse `/case add`, or right-click a message or member and choose **Apps → Add case** (or **Add case for member**). Pick the rule and the case is created immediately.\n\nEvidence is optional. Attach a file or message link when adding the case, or use `/case evidence` later. **Edit context** adds staff notes; **View evidence** opens saved copies. Those details stay private to staff. Check the copy result before deleting the source.\n\nMade a mistake? **Void case** keeps the record but stops it counting against the member. It also queues removal of that case’s ban or timeout when possible. `/case view` shows the result.\n\nUse `/case list` for recent cases, `/case user` for a member’s history, and `/case failures` when an action needs attention."
		case "appeals":
			body = "## Appeals\nMembers can appeal from their case DM, even after a ban. Each case gets one appeal.\n\nSet the staff channel with `/setup appeals`. Review requests there, or find them with `/appeals`. Accepting voids the case and starts removing its ban or timeout. Rejecting keeps the case. The member gets the decision by DM.\n\nAdd a server invite with the `rejoin` option on `/setup appeals` so an unbanned member can return. Turn appeals on or off for a rule with `/template edit`."
		case "setup":
			body = "## Set up your server\nRun a setup command without channel options and I’ll create what it needs, or reuse its saved channels. You can choose existing channels instead.\n\n`/setup tickets` creates a support panel and staff queue. Members open a private thread, chat with staff, then close it. The transcript is saved before the thread disappears and sent to the member by DM when possible.\n\n`/setup honeypot` creates a trap channel with a warning. Check its rule before using it: messages there trigger the configured punishment. Moderators are exempt.\n\n`/setup logging` records Discord events. `/setup audit` records Quack’s moderation activity.\n\nFor tickets, honeypot, or logging, use `enabled:false` to pause the feature and `enabled:true` to turn it back on. Use that option on its own; saved channels are kept."
		}
		return ui.Immediate(ui.Public(ui.Content(body, false)))
	}}
}
