package ui

import (
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// commandMentionRegistry stores Discord's current IDs per application. Sync owns
// writes; message rendering only reads the immutable path snapshots under a lock.
var commandMentionRegistry = struct {
	sync.RWMutex
	applications map[string]map[string]string
}{applications: make(map[string]map[string]string)}

// SetCommandMentions replaces an application's snapshot from the command list
// already fetched by startup sync, removing IDs left behind by earlier syncs.
func SetCommandMentions(applicationID string, commands []*discordgo.ApplicationCommand) {
	paths := make(map[string]string)
	for _, command := range commands {
		addCommandMentionPaths(paths, command, "")
	}
	commandMentionRegistry.Lock()
	commandMentionRegistry.applications[applicationID] = paths
	commandMentionRegistry.Unlock()
}

// RegisterCommandMentions records the actual ID returned by Discord with the
// successfully synced definition. Child paths share their root command's ID.
func RegisterCommandMentions(applicationID string, command *discordgo.ApplicationCommand, commandID string) {
	if command == nil || applicationID == "" || commandID == "" {
		return
	}
	commandMentionRegistry.Lock()
	defer commandMentionRegistry.Unlock()
	paths := commandMentionRegistry.applications[applicationID]
	if paths == nil {
		paths = make(map[string]string)
		commandMentionRegistry.applications[applicationID] = paths
	}
	removeCommandMentionPaths(paths, command.Name)
	addCommandMentionPaths(paths, command, commandID)
}

// RemoveCommandMentions drops references only after Discord confirms deletion.
func RemoveCommandMentions(applicationID, name string) {
	commandMentionRegistry.Lock()
	defer commandMentionRegistry.Unlock()
	removeCommandMentionPaths(commandMentionRegistry.applications[applicationID], name)
}

// removeCommandMentionPaths removes a root and every child without affecting
// commands whose names merely share a prefix.
func removeCommandMentionPaths(paths map[string]string, root string) {
	for path := range paths {
		if path == root || strings.HasPrefix(path, root+" ") {
			delete(paths, path)
		}
	}
}

// addCommandMentionPaths includes only executable slash paths, excluding context
// menu commands and subcommand groups that cannot be invoked on their own.
func addCommandMentionPaths(paths map[string]string, command *discordgo.ApplicationCommand, id string) {
	if command == nil || (command.Type != 0 && command.Type != discordgo.ChatApplicationCommand) {
		return
	}
	if id == "" {
		id = command.ID
	}
	if id == "" || command.Name == "" {
		return
	}
	var walk func(string, []*discordgo.ApplicationCommandOption)
	walk = func(path string, options []*discordgo.ApplicationCommandOption) {
		children := false
		for _, option := range options {
			if option == nil {
				continue
			}
			if option.Type == discordgo.ApplicationCommandOptionSubCommand ||
				option.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
				children = true
				walk(path+" "+option.Name, option.Options)
			}
		}
		if !children {
			paths[path] = id
		}
	}
	walk(command.Name, command.Options)
}

// ResolveCommandMentions turns known command references into native clickable
// mentions without any Discord requests. Unknown references, fenced code, URLs,
// existing mentions, and escaped user inline code remain literal. Parameters
// stay outside the mention because Discord's syntax accepts only the path and ID.
func ResolveCommandMentions(content, applicationID string) string {
	commandMentionRegistry.RLock()
	registry := commandMentionRegistry.applications[applicationID]
	paths := make([]string, 0, len(registry))
	ids := make(map[string]string, len(registry))
	for path, id := range registry {
		paths = append(paths, path)
		ids[path] = id
	}
	commandMentionRegistry.RUnlock()
	if len(paths) == 0 {
		return content
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	var out strings.Builder
	for index := 0; index < len(content); {
		if strings.HasPrefix(content[index:], "\\`") {
			end := strings.Index(content[index+2:], "\\`")
			if end < 0 {
				out.WriteString(content[index:])
				break
			}
			end += index + 4
			out.WriteString(content[index:end])
			index = end
			continue
		}
		if content[index] == '`' {
			width := 1
			for index+width < len(content) && content[index+width] == '`' {
				width++
			}
			end := strings.Index(content[index+width:], strings.Repeat("`", width))
			if end < 0 {
				out.WriteString(content[index:])
				break
			}
			end += index + width
			inside := content[index+width : end]
			replacement, consumed := matchCommandMention(inside, paths, ids)
			if width == 1 && consumed > 0 {
				out.WriteString(replacement)
				if tail := strings.TrimSpace(inside[consumed:]); tail != "" {
					out.WriteString(" `" + tail + "`")
				}
			} else {
				out.WriteString(content[index : end+width])
			}
			index = end + width
			continue
		}
		if content[index] == '<' {
			if end := strings.IndexByte(content[index:], '>'); end >= 0 {
				out.WriteString(content[index : index+end+1])
				index += end + 1
				continue
			}
		}
		if content[index] == '/' && (index == 0 || strings.ContainsRune(" \t\n([\"'", rune(content[index-1]))) {
			if replacement, consumed := matchCommandMention(content[index:], paths, ids); consumed > 0 {
				out.WriteString(replacement)
				index += consumed
				continue
			}
		}
		out.WriteByte(content[index])
		index++
	}
	return out.String()
}

// matchCommandMention resolves the longest complete path so /case view never
// consumes /case viewer and nested command groups retain their full names.
func matchCommandMention(content string, paths []string, ids map[string]string) (string, int) {
	for _, path := range paths {
		prefix := "/" + path
		if !strings.HasPrefix(content, prefix) {
			continue
		}
		if len(content) > len(prefix) {
			next, _ := utf8.DecodeRuneInString(content[len(prefix):])
			if unicode.IsLetter(next) || unicode.IsNumber(next) || unicode.IsMark(next) || next == '-' || next == '_' || next == '/' {
				continue
			}
		}
		return "</" + path + ":" + ids[path] + ">", len(prefix)
	}
	return "", 0
}
