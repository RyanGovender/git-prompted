package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryangovender/git-prompted/assets"
	"github.com/ryangovender/git-prompted/internal/core"
)

func setup(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GIT_PROMPTED_HOME", home)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("GEMINI_PROJECT_DIR", "")
	if err := os.WriteFile(filepath.Join(home, "goals.json"), assets.GoalsExample, 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func hook(t *testing.T, agent, event, payload string) string {
	t.Helper()
	var out bytes.Buffer
	if code := runHook([]string{agent, event}, strings.NewReader(payload), &out); code != 0 {
		t.Fatalf("hook exit %d", code)
	}
	return strings.TrimSpace(out.String())
}

func cmd(t *testing.T, args ...string) {
	t.Helper()
	if err := runCommand(args); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
}

func TestFullFlowPerAgent(t *testing.T) {
	edits := map[string]string{
		"claude": `{"session_id":"%s","cwd":"/p","tool_name":"Write","tool_input":{"file_path":"/p/w.go","content":"go func() { ch <- 1 }()"}}`,
		"codex":  `{"session_id":"%s","cwd":"/p","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: w.go\n+go func() { ch <- 1 }()\n*** End Patch"}}`,
		"gemini": `{"session_id":"%s","cwd":"/p","tool_name":"write_file","tool_input":{"file_path":"/p/w.go","content":"go func() { ch <- 1 }()"}}`,
	}
	arch := `{"session_id":"%s","cwd":"/p","tool_name":"Edit","tool_input":{"file_path":"/p/port.go","new_string":"type Store interface { Get() }"}}`
	for agent, tmpl := range edits {
		t.Run(agent, func(t *testing.T) {
			setup(t)
			sid := agent + "-s1"
			p := func(s string) string { return strings.Replace(s, "%s", sid, 1) }

			start := hook(t, agent, "session-start", `{"session_id":"`+sid+`","source":"startup"}`)
			if !strings.Contains(start, "Learning checkpoints on for this session") {
				t.Fatalf("session start: %s", start)
			}
			if agent == "codex" && !strings.Contains(start, "Frame, don't solve") {
				t.Fatal("codex should get the coaching guide injected")
			}

			deny := hook(t, agent, "before-edit", p(tmpl))
			if !strings.Contains(deny, "Learning checkpoint: Concurrency") {
				t.Fatalf("expected checkpoint, got: %s", deny)
			}
			cmd(t, "decide", "--session", sid, "--topic", "concurrency", "--choice", "skip")
			if out := hook(t, agent, "before-edit", p(tmpl)); out != "" && out != "{}" {
				t.Fatalf("skip should allow retry, got %s", out)
			}

			if agent != "gemini" && agent != "codex" { // quiz-after flow via Claude's Edit tool
				if out := hook(t, agent, "before-edit", p(arch)); out != "" {
					t.Fatalf("quiz-after must not block: %s", out)
				}
				stop := hook(t, agent, "after-turn", `{"session_id":"`+sid+`","stop_hook_active":false}`)
				var m map[string]any
				json.Unmarshal([]byte(stop), &m)
				if m["decision"] != "block" || !strings.Contains(m["reason"].(string), "Architecture") {
					t.Fatalf("stop: %s", stop)
				}
				if again := hook(t, agent, "after-turn", `{"session_id":"`+sid+`","stop_hook_active":true}`); again != "" {
					t.Fatal("must not loop")
				}
			}
		})
	}
}

func TestSessionOffAndMuteAndProgress(t *testing.T) {
	home := setup(t)
	edit := `{"session_id":"s2","tool_name":"Write","tool_input":{"file_path":"x.py","content":"import asyncio\nasyncio.gather()"}}`
	hook(t, "claude", "session-start", `{"session_id":"s2","source":"startup"}`)
	cmd(t, "session", "off", "--session", "s2")
	if out := hook(t, "claude", "before-edit", edit); out != "" {
		t.Fatalf("session off should be silent: %s", out)
	}
	if out := hook(t, "claude", "session-start", `{"session_id":"s2","source":"resume"}`); out != "" {
		t.Fatalf("explicit off should stay quiet on resume: %s", out)
	}
	cmd(t, "decide", "--session", "s3", "--topic", "concurrency", "--choice", "mute")
	if out := hook(t, "claude", "before-edit", strings.Replace(edit, "s2", "s3", 1)); out != "" {
		t.Fatalf("muted topic fired: %s", out)
	}
	cmd(t, "done", "--session", "s3", "--topic", "concurrency", "--result", "partial", "--note", "missed ctx cancellation")
	raw, _ := os.ReadFile(filepath.Join(home, "progress.md"))
	if !strings.Contains(string(raw), "partial") || !strings.Contains(string(raw), "missed ctx cancellation") {
		t.Fatalf("progress: %s", raw)
	}
}

func TestCapAndRobustness(t *testing.T) {
	home := setup(t)
	os.WriteFile(filepath.Join(home, "goals.json"), []byte(`{"start_prompt":"on","max_checkpoints_per_session":2,
	  "topics":[{"id":"a","patterns":["AAA"]},{"id":"b","patterns":["BBB"]},{"id":"c","patterns":["CCC"]},{"id":"bad","patterns":["(?<=x)"]}]}`), 0o644)
	fired := 0
	for _, x := range []string{"AAA", "BBB", "CCC"} {
		if hook(t, "claude", "before-edit", `{"session_id":"cap","tool_name":"Edit","tool_input":{"file_path":"f","new_string":"`+x+`"}}`) != "" {
			fired++
		}
	}
	if fired != 2 {
		t.Fatalf("cap: fired %d", fired)
	}
	os.WriteFile(filepath.Join(home, "goals.json"), []byte(`{broken`), 0o644)
	if out := hook(t, "gemini", "before-edit", `{"tool_name":"write_file","tool_input":{"content":"go func"}}`); out != "{}" {
		t.Fatalf("broken config should be a no-op: %s", out)
	}
	if out := hook(t, "claude", "before-edit", `not json`); out != "" {
		t.Fatalf("bad stdin: %s", out)
	}
}

func TestProjectConfigMergesAndPathGlobs(t *testing.T) {
	setup(t)
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, ".git-prompted.json"), []byte(`{"start_prompt":"on","topics":[{"id":"hexagonal","name":"Ports & adapters","paths":["internal/adapters/**"]}]}`), 0o644)
	cfg := core.LoadConfig(proj, func(string) {})
	if cfg == nil || cfg.Topic("hexagonal") == nil || cfg.Topic("concurrency") == nil || cfg.StartPrompt != "on" {
		t.Fatal("merge failed")
	}
	if !cfg.Topic("hexagonal").Matches([]string{"internal/adapters/http/server.go"}, "") ||
		cfg.Topic("hexagonal").Matches([]string{"internal/domain/x.go"}, "") {
		t.Fatal("path glob")
	}
}

func TestInstallCodexIsIdempotentAndPreservesOtherHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other-guard"}]}]}}`), 0o644)
	for i := 0; i < 2; i++ {
		if err := installCodex(true, "/opt/git-prompted"); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "hooks.json"))
	s := string(raw)
	if strings.Count(s, "hook codex before-edit") != 1 || !strings.Contains(s, "other-guard") || !strings.Contains(s, "hook codex after-turn") {
		t.Fatalf("install result:\n%s", s)
	}
	installCodex(false, "/opt/git-prompted")
	raw, _ = os.ReadFile(filepath.Join(dir, "hooks.json"))
	if strings.Contains(string(raw), "git-prompted") || !strings.Contains(string(raw), "other-guard") {
		t.Fatalf("uninstall result:\n%s", raw)
	}
}

func TestConceptTagsFlowIntoPromptsAndStats(t *testing.T) {
	setup(t)
	edit := `{"session_id":"tags","tool_name":"Write","tool_input":{"file_path":"w.go","content":"go func(){}()"}}`
	cmd(t, "done", "--session", "tags", "--topic", "concurrency", "--result", "partial",
		"--got", "channel-ownership", "--missed", "Cancellation, goroutine leak", "--note", "forgot ctx")
	raw, _ := os.ReadFile(filepath.Join(os.Getenv("GIT_PROMPTED_HOME"), "log.jsonl"))
	if !strings.Contains(string(raw), `"missed":["cancellation","goroutine-leak"]`) {
		t.Fatalf("log: %s", raw)
	}
	// Pretend the cooldown passed and the review is due, then trigger a checkpoint.
	core.WithState(func(s *core.State) error { s.Cooldowns = map[string]float64{}; return nil })
	backdateLog(t, 2*86400)
	out := hook(t, "claude", "before-edit", edit)
	for _, want := range []string{"Concept tags for concurrency", "goroutine-lifetime", "Due for review", "cancellation", "--got", "--missed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("prompt missing %q:\n%s", want, out)
		}
	}
}

func backdateLog(t *testing.T, secs float64) {
	t.Helper()
	path := filepath.Join(os.Getenv("GIT_PROMPTED_HOME"), "log.jsonl")
	var lines []string
	for _, e := range core.ReadLog() {
		e.TS -= secs
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func TestStatusLineCommandWrapsExisting(t *testing.T) {
	setup(t)
	cmd(t, "done", "--session", "s", "--topic", "concurrency", "--result", "nailed", "--got", "cancellation")
	var out bytes.Buffer
	in := strings.NewReader(`{"session_id":"s","workspace":{"current_dir":"/tmp"}}`)
	if err := statusLine(in, &out, false, `cat >/dev/null; echo "main ✓"`); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "main ✓  │  🧠 concurrency 100%" {
		t.Fatalf("status line: %q", got)
	}
}

func TestInstallStatusLineWrapsAndRestores(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	settings := filepath.Join(dir, "settings.json")
	os.WriteFile(settings, []byte(`{"model":"opus","statusLine":{"type":"command","command":"my-line --fancy"}}`), 0o644)
	if err := installStatusLine(true, "/home/u/.git-prompted/bin/git-prompted"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(settings)
	if !strings.Contains(string(raw), `statusline --with 'my-line --fancy'`) || !strings.Contains(string(raw), `"model": "opus"`) {
		t.Fatalf("installed: %s", raw)
	}
	installStatusLine(true, "/x") // idempotent
	installStatusLine(false, "")
	raw, _ = os.ReadFile(settings)
	if strings.Contains(string(raw), "git-prompted") || !strings.Contains(string(raw), `"command": "my-line --fancy"`) {
		t.Fatalf("restored: %s", raw)
	}
}
