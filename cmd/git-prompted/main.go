// Command git-prompted adds opt-in learning checkpoints to coding agents
// (Claude Code, OpenAI Codex CLI, Gemini CLI). It never calls a model itself.
// Installed on PATH as git-prompted, git also exposes it as `git prompted`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ryangovender/git-prompted/assets"
	"github.com/ryangovender/git-prompted/internal/adapter"
	"github.com/ryangovender/git-prompted/internal/core"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const usage = `git prompted: the AI prompts you back. Learning checkpoints for coding agents.

Hooks (called by the agent CLI, JSON on stdin):
  git-prompted hook <claude|codex|gemini> <session-start|before-edit|after-turn>

Commands:
  git-prompted init [--defaults|--empty]    how to pick topics (--defaults: concurrency + architecture; --empty: none)
  git-prompted topics list [--json]         built-in topic catalog; * = in your goals.json
  git-prompted topics add <id>...           add catalog topics (add --json - reads custom topic JSON from stdin)
  git-prompted topics remove <id>...
  git-prompted status                       topics, cooldowns, mutes
  git-prompted progress                     write and print the path of progress.md
  git-prompted session on|off               checkpoints for the current session
  git-prompted decide --choice skip|session-off|mute [--topic ID]
  git-prompted done --topic ID --result nailed|partial|missed [--got TAGS] [--missed TAGS] [--note TEXT]
  git-prompted stats [--json]             weak spots, trends and reviews due (JSON feeds dashboards)
  git-prompted statusline [--plain] [--with CMD]   one-line summary for Claude Code's status line
  git-prompted unmute --topic ID
  git-prompted coaching                     print the coaching guide
  git-prompted install codex [--command PATH]        add hooks to ~/.codex/hooks.json
  git-prompted install statusline [--command PATH]   show progress in Claude Code's status line
  git-prompted uninstall codex|statusline
  git-prompted version

Commands that touch a session accept --session ID (default: most recent session).
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(usage)
		return
	}
	if args[0] == "hook" {
		os.Exit(runHook(args[1:], os.Stdin, os.Stdout))
	}
	if err := runCommand(args); err != nil {
		fmt.Fprintln(os.Stderr, "git-prompted:", err)
		os.Exit(1)
	}
}

func warn(msg string) { fmt.Fprintln(os.Stderr, "git-prompted:", msg) }

// stablePath is the symlink the launcher keeps pointing at the current binary,
// so configs that store a path (status line, Codex hooks) survive upgrades.
func stablePath() string {
	p := filepath.Join(core.Home(), "bin", "git-prompted")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return exePath()
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "git-prompted"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// runHook never fails the agent: whatever happens, it exits 0 and prints a
// valid (possibly no-op) response. A learning tool must not break a coding session.
func runHook(args []string, in io.Reader, out io.Writer) (code int) {
	if len(args) < 2 {
		warn("usage: hook <agent> <event>")
		return 0
	}
	ad, ok := adapter.Get(args[0])
	if !ok {
		warn("unknown agent " + args[0])
		return 0
	}
	var resp []byte
	defer func() {
		if r := recover(); r != nil {
			warn(fmt.Sprintf("hook error ignored: %v", r))
			resp = ad.Noop()
		}
		if len(resp) > 0 {
			out.Write(resp)
			out.Write([]byte("\n"))
		}
		code = 0
	}()
	resp = ad.Noop()

	raw, _ := io.ReadAll(in)
	h := adapter.Hook(args[1])
	ev, relevant := ad.Parse(h, raw)
	if !relevant {
		return 0
	}
	cfg := core.LoadConfig(ev.ProjectDir, warn)
	if cfg == nil {
		return 0
	}
	eng := &core.Engine{Cfg: cfg, Exe: stablePath(), Coaching: assets.Coaching}
	var text string
	var err error
	switch h {
	case adapter.SessionStart:
		if text, err = eng.SessionStart(ev); err == nil && text != "" {
			resp = ad.Context(text)
		}
	case adapter.BeforeEdit:
		if text, err = eng.BeforeEdit(ev); err == nil && text != "" {
			resp = ad.Deny(text)
		}
	case adapter.AfterTurn:
		if text, err = eng.AfterTurn(ev); err == nil && text != "" {
			resp = ad.Continue(text)
		}
	default:
		warn("unknown hook " + string(h))
	}
	if err != nil {
		warn("hook error ignored: " + err.Error())
	}
	return 0
}

// splitArgs lets positionals and flags appear in any order.
func splitArgs(args []string) (pos, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return
}

func runCommand(args []string) error {
	name := args[0]
	var pos, flagArgs []string
	switch name {
	case "--version", "-v", "--help", "-h":
	default:
		// Flags may also come before the command (`--session ID done ...`), the form prompts print.
		pos, flagArgs = splitArgs(args)
		if len(pos) == 0 {
			return fmt.Errorf("no command given (try: git-prompted help)")
		}
		name, pos = pos[0], pos[1:]
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	session := fs.String("session", "", "session id")
	topic := fs.String("topic", "", "topic id")
	choice := fs.String("choice", "", "skip | session-off | mute")
	result := fs.String("result", "", "nailed | partial | missed")
	note := fs.String("note", "", "one-line note")
	command := fs.String("command", "", "binary path to register (install)")
	got := fs.String("got", "", "comma-separated concept tags the user got right")
	missed := fs.String("missed", "", "comma-separated concept tags the user missed")
	asJSON := fs.Bool("json", false, "machine-readable output")
	plain := fs.Bool("plain", false, "no emoji")
	with := fs.String("with", "", "status line command to combine with")
	defaults := fs.Bool("defaults", false, "init: start with the default topics")
	empty := fs.Bool("empty", false, "init: start with no topics")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	now := float64(time.Now().UnixNano()) / 1e9

	switch name {
	case "version", "--version", "-v":
		fmt.Println("git-prompted", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	case "coaching":
		fmt.Print(assets.Coaching)
		return nil
	case "init":
		path := core.UserConfigPath()
		if _, err := os.Stat(path); err == nil {
			fmt.Println("Config already exists:", path)
			return nil
		}
		catalog, err := core.ParseCatalog(assets.Topics)
		if err != nil {
			return err
		}
		switch {
		case *defaults && *empty:
			return fmt.Errorf("use --defaults or --empty, not both")
		case *defaults:
			picked, err := core.CatalogTopics(catalog, core.DefaultTopicIDs)
			if err != nil {
				return err
			}
			if _, _, err := core.AddTopics(picked); err != nil {
				return err
			}
			fmt.Printf("Created %s with %s.\n", path, strings.Join(core.DefaultTopicIDs, ", "))
		case *empty:
			if _, _, err := core.AddTopics(nil); err != nil {
				return err
			}
			fmt.Printf("Created %s with no topics. Pick some any time: /git-prompted:learn init, or git-prompted topics add <id>...\n", path)
		default:
			fmt.Println("Pick the topics you want to practise. In your agent, run /git-prompted:learn init; or from here:")
			fmt.Println()
			for _, t := range catalog {
				fmt.Printf("  %-16s %s\n", t.ID, t.DisplayName())
			}
			fmt.Println("\n  git-prompted topics add <id>...    e.g. git-prompted topics add concurrency error-handling")
		}
		return nil
	case "topics":
		return runTopics(os.Stdout, stdin, pos, *asJSON)
	case "status":
		return status(os.Stdout, cwd, *session)
	case "progress":
		path, err := core.WriteProgress()
		if err == nil {
			fmt.Println(path)
		}
		return err
	case "unmute":
		if *topic == "" {
			return fmt.Errorf("--topic required")
		}
		return core.WithState(func(s *core.State) error {
			s.Unmute(*topic)
			fmt.Println("Unmuted", *topic)
			return nil
		})
	case "stats":
		sum := core.Summarize(core.ReadLog(), now)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sum)
		}
		printStats(sum)
		return nil
	case "statusline":
		return statusLine(os.Stdin, os.Stdout, *plain, *with)
	case "install", "uninstall":
		cmdPath := *command
		if cmdPath == "" {
			cmdPath = stablePath()
		}
		if len(pos) == 1 && pos[0] == "codex" {
			return installCodex(name == "install", cmdPath)
		}
		if len(pos) == 1 && pos[0] == "statusline" {
			return installStatusLine(name == "install", cmdPath)
		}
		return fmt.Errorf("usage: git-prompted %s codex|statusline (Claude Code and Gemini CLI install as plugin/extension)", name)
	}

	// Session-scoped commands.
	return core.WithState(func(s *core.State) error {
		sid := *session
		if sid == "" {
			sid = s.CurrentSession
		}
		if sid == "" {
			return fmt.Errorf("no session known yet; pass --session")
		}
		sess := s.Session(sid, "", "", now)
		entry := core.LogEntry{Session: sid, Agent: sess.Agent, Project: filepath.Base(cwd), TS: now, Topic: *topic}
		switch name {
		case "session":
			if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
				return fmt.Errorf("usage: git-prompted session on|off")
			}
			sess.Mode, sess.Explicit = pos[0], true
			fmt.Printf("git-prompted checkpoints %s for this session.\n", pos[0])
			return nil
		case "decide":
			switch *choice {
			case "session-off":
				sess.Mode, sess.Explicit = "off", true
				fmt.Print("Checkpoints off for this session.")
			case "skip", "mute":
				if *topic == "" {
					return fmt.Errorf("--topic required for --choice %s", *choice)
				}
				if *choice == "mute" {
					s.Mute(*topic)
					fmt.Printf("Muted %s (undo: git-prompted unmute --topic %s).", *topic, *topic)
				} else {
					s.Cooldowns[*topic] = now
					fmt.Printf("Skipped %s; it is on cooldown.", *topic)
				}
			default:
				return fmt.Errorf("--choice must be skip, session-off or mute")
			}
			fmt.Println(" Retry the edit.")
			entry.Event = *choice
			return core.AppendLog(entry)
		case "done":
			if *topic == "" || (*result != "nailed" && *result != "partial" && *result != "missed") {
				return fmt.Errorf("usage: git-prompted done --topic ID --result nailed|partial|missed [--got TAGS] [--missed TAGS] [--note TEXT]")
			}
			s.Cooldowns[*topic] = now
			entry.Event, entry.Result, entry.Note = "done", *result, *note
			entry.Got, entry.Missed = core.SplitTags(*got), core.SplitTags(*missed)
			if err := core.AppendLog(entry); err != nil {
				return err
			}
			path, err := core.WriteProgress()
			if err != nil {
				return err
			}
			fmt.Printf("Logged %s for %s. Progress: %s. Retry the edit.\n", *result, *topic, path)
			if len(entry.Got)+len(entry.Missed) == 0 {
				fmt.Println("Tip: add --got/--missed concept tags so weak spots and reviews can be tracked.")
			}
			return nil
		}
		return fmt.Errorf("unknown command %q (try: git-prompted help)", name)
	})
}

func status(w io.Writer, cwd, session string) error {
	s := core.ReadState()
	if session == "" {
		session = s.CurrentSession
	}
	if sess, ok := s.Sessions[session]; ok {
		fmt.Fprintf(w, "Session %s (%s): checkpoints %s, %d asked\n", session, sess.Agent, sess.Mode, sess.Checkpoints)
	} else {
		fmt.Fprintln(w, "No active session yet.")
	}
	cfg := core.LoadConfig(cwd, warn)
	if cfg == nil {
		fmt.Fprintln(w, "No topics configured. Pick some: /git-prompted:learn init in your agent, or git-prompted topics add <id>... (see git-prompted topics list)")
		return nil
	}
	now := float64(time.Now().Unix())
	for _, t := range cfg.Topics {
		mode := "ask-first"
		if t.QuizAfter() {
			mode = "quiz-after"
		}
		flag := "active"
		if s.IsMuted(t.ID) {
			flag = "muted"
		} else if left := cfg.Cooldown().Seconds() - (now - s.Cooldowns[t.ID]); left > 0 {
			flag = fmt.Sprintf("cooling down %dm", int(left/60))
		}
		fmt.Fprintf(w, "  %-16s %-12s %s\n", t.ID, mode, flag)
	}
	return nil
}

// installCodex merges (or removes) git-prompted entries in ~/.codex/hooks.json,
// leaving any other hooks untouched and keeping a backup.
func installCodex(install bool, cmdPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".codex")
	if v := os.Getenv("CODEX_HOME"); v != "" {
		dir = v
	}
	path := filepath.Join(dir, "hooks.json")
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		backup := fmt.Sprintf("%s.bak-%d", path, time.Now().Unix())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return err
		}
		fmt.Println("Backed up existing hooks to", backup)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	events := map[string]string{"SessionStart": "session-start", "PreToolUse": "before-edit", "Stop": "after-turn"}
	keys := make([]string, 0, len(events))
	for k := range events {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, event := range keys {
		var kept []any
		groups, _ := hooks[event].([]any)
		for _, g := range groups {
			if !isOurs(g) {
				kept = append(kept, g)
			}
		}
		if install {
			// No matcher on PreToolUse: the adapter filters to edit tools itself,
			// which is robust to tool-name changes across Codex versions.
			kept = append(kept, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": fmt.Sprintf("%s hook codex %s", quoteCmd(cmdPath), events[event]), "timeout": 10,
			}}})
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	doc["hooks"] = hooks
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if install {
		fmt.Printf("Installed git-prompted hooks in %s.\n", path)
		fmt.Println("Next: make sure hooks are enabled in your Codex config (see https://developers.openai.com/codex/hooks),")
		fmt.Println("start a new Codex session, and approve the hooks if Codex asks you to trust them.")
		fmt.Println("Then run `git-prompted init` if you have no goals yet.")
	} else {
		fmt.Printf("Removed git-prompted hooks from %s.\n", path)
	}
	return nil
}

func isOurs(group any) bool {
	g, _ := group.(map[string]any)
	hs, _ := g["hooks"].([]any)
	for _, h := range hs {
		m, _ := h.(map[string]any)
		if c, _ := m["command"].(string); strings.Contains(c, "hook codex") && strings.Contains(c, "git-prompted") {
			return true
		}
	}
	return false
}

func quoteCmd(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}
