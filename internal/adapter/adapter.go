// Package adapter translates each agent CLI's hook payloads to core.Event and
// formats responses the way that CLI expects. Adding an agent = one Adapter.
package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryangovender/git-prompted/internal/core"
)

// Hook is the normalised hook kind (the CLI's native event is mapped in its hooks config).
type Hook string

const (
	SessionStart Hook = "session-start"
	BeforeEdit   Hook = "before-edit"
	AfterTurn    Hook = "after-turn"
)

// Adapter is one agent CLI.
type Adapter interface {
	Name() string
	// Parse returns the event and whether this call is relevant (e.g. an edit tool).
	Parse(h Hook, raw []byte) (core.Event, bool)
	Context(text string) []byte // SessionStart: inject context
	Deny(reason string) []byte  // BeforeEdit: block the tool, show reason to the model
	Continue(reason string) []byte
	Noop() []byte
}

// Get returns the adapter for an agent name.
func Get(name string) (Adapter, bool) {
	switch name {
	case "claude":
		return claude{}, true
	case "codex":
		return codex{}, true
	case "gemini":
		return gemini{}, true
	}
	return nil, false
}

// payload holds the fields the three CLIs share (they converged on similar shapes).
type payload struct {
	SessionID      string          `json:"session_id"`
	Cwd            string          `json:"cwd"`
	Source         string          `json:"source"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`
}

type toolInput struct {
	FilePath  string `json:"file_path"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	NewString string `json:"new_string"`
	Edits     []struct {
		NewString string `json:"new_string"`
	} `json:"edits"`
	Command json.RawMessage `json:"command"` // Codex apply_patch: patch text (string; occasionally argv array)
	Patch   string          `json:"patch"`
	Input   string          `json:"input"`
}

func parseCommon(raw []byte, agent string, envProjectDir string) (payload, toolInput, core.Event) {
	var p payload
	_ = json.Unmarshal(raw, &p)
	var ti toolInput
	if len(p.ToolInput) > 0 {
		_ = json.Unmarshal(p.ToolInput, &ti)
	}
	dir := os.Getenv(envProjectDir)
	if dir == "" {
		dir = p.Cwd
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	sid := p.SessionID
	if sid == "" {
		sid = "unknown"
	}
	return p, ti, core.Event{
		Agent: agent, SessionID: sid, ProjectDir: dir,
		Source: p.Source, StopHookActive: p.StopHookActive,
	}
}

func relPath(projectDir, path string) string {
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		if r, err := filepath.Rel(projectDir, path); err == nil && !strings.HasPrefix(r, "..") {
			path = r
		}
	}
	return filepath.ToSlash(path)
}

func directEdit(ev *core.Event, ti toolInput) {
	parts := []string{ti.Content, ti.NewString}
	for _, e := range ti.Edits {
		parts = append(parts, e.NewString)
	}
	ev.Code = strings.Join(parts, "\n")
	path := ti.FilePath
	if path == "" {
		path = ti.Path
	}
	if p := relPath(ev.ProjectDir, path); p != "" {
		ev.Files = []string{p}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ---------------------------------------------------------------- Claude Code

type claude struct{}

func (claude) Name() string { return "claude" }

func (claude) Parse(h Hook, raw []byte) (core.Event, bool) {
	p, ti, ev := parseCommon(raw, "claude", "CLAUDE_PROJECT_DIR")
	if h == BeforeEdit {
		switch p.ToolName {
		case "Edit", "Write", "MultiEdit":
			directEdit(&ev, ti)
		default:
			return ev, false
		}
	}
	return ev, true
}

func (claude) Context(text string) []byte {
	return mustJSON(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "additionalContext": text}})
}

func (claude) Deny(reason string) []byte {
	return mustJSON(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason}})
}

func (claude) Continue(reason string) []byte {
	return mustJSON(map[string]any{"decision": "block", "reason": reason})
}

func (claude) Noop() []byte { return nil }

// ---------------------------------------------------------------- OpenAI Codex CLI

// Codex mirrors Claude Code's hook protocol, but edits arrive as apply_patch text.
type codex struct{ claude }

func (codex) Name() string { return "codex" }

func (codex) Parse(h Hook, raw []byte) (core.Event, bool) {
	p, ti, ev := parseCommon(raw, "codex", "CODEX_PROJECT_DIR")
	if h != BeforeEdit {
		return ev, true
	}
	switch strings.TrimPrefix(p.ToolName, "functions.") {
	case "apply_patch", "Edit", "Write":
	default:
		return ev, false
	}
	patch := ti.Patch
	if patch == "" {
		patch = ti.Input
	}
	if patch == "" && len(ti.Command) > 0 {
		var s string
		if json.Unmarshal(ti.Command, &s) == nil {
			patch = s
		} else {
			var argv []string
			if json.Unmarshal(ti.Command, &argv) == nil {
				patch = strings.Join(argv, "\n")
			}
		}
	}
	if patch == "" {
		directEdit(&ev, ti) // alias tools with plain edit fields
		return ev, ev.Code != ""
	}
	files, code := ParsePatch(patch)
	for _, f := range files {
		ev.Files = append(ev.Files, relPath(ev.ProjectDir, f))
	}
	ev.Code = code
	return ev, true
}

// ParsePatch extracts touched files and added lines from Codex's apply_patch
// format ("*** Update File: x") or a unified diff ("+++ b/x").
func ParsePatch(patch string) (files []string, added string) {
	var b strings.Builder
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "*** Add File: "), strings.HasPrefix(line, "*** Update File: "),
			strings.HasPrefix(line, "*** Move to: "):
			files = append(files, strings.TrimSpace(line[strings.Index(line, ": ")+2:]))
		case strings.HasPrefix(line, "+++ "):
			f := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			if f != "/dev/null" {
				files = append(files, strings.TrimPrefix(f, "b/"))
			}
		case strings.HasPrefix(line, "+"):
			b.WriteString(line[1:])
			b.WriteByte('\n')
		}
	}
	return files, b.String()
}

// ---------------------------------------------------------------- Gemini CLI

type gemini struct{}

func (gemini) Name() string { return "gemini" }

func (gemini) Parse(h Hook, raw []byte) (core.Event, bool) {
	p, ti, ev := parseCommon(raw, "gemini", "GEMINI_PROJECT_DIR")
	if h == BeforeEdit {
		switch p.ToolName {
		case "write_file", "replace", "edit":
			directEdit(&ev, ti)
		default:
			return ev, false
		}
	}
	return ev, true
}

func (gemini) Context(text string) []byte {
	return mustJSON(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "additionalContext": text}})
}

func (gemini) Deny(reason string) []byte {
	return mustJSON(map[string]any{"decision": "deny", "reason": reason})
}

// AfterAgent "deny" sends reason back to the model as a new prompt.
func (gemini) Continue(reason string) []byte {
	return mustJSON(map[string]any{"decision": "deny", "reason": reason})
}

// Gemini parses stdout as JSON, so a no-op is an empty object.
func (gemini) Noop() []byte { return []byte("{}") }
