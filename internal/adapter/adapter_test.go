package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePatch(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: internal/worker.go\n@@\n-old()\n+go func() {\n+\tch <- 1\n+}()\n*** Add File: pkg/store.go\n+package store\n*** End Patch"
	files, code := ParsePatch(patch)
	if len(files) != 2 || files[0] != "internal/worker.go" || files[1] != "pkg/store.go" {
		t.Fatalf("files = %v", files)
	}
	if !strings.Contains(code, "go func()") || strings.Contains(code, "old()") {
		t.Fatalf("code = %q", code)
	}
	files, code = ParsePatch("--- a/x.go\n+++ b/x.go\n@@\n+var mu sync.Mutex\n")
	if len(files) != 1 || files[0] != "x.go" || !strings.Contains(code, "sync.Mutex") {
		t.Fatalf("unified diff: %v %q", files, code)
	}
}

func TestAdaptersParseEdits(t *testing.T) {
	cases := []struct {
		agent, raw, wantFile, wantCode string
		relevant                       bool
	}{
		{"claude", `{"session_id":"s","cwd":"/p","tool_name":"Write","tool_input":{"file_path":"/p/a.go","content":"go func(){}()"}}`, "a.go", "go func", true},
		{"claude", `{"session_id":"s","tool_name":"MultiEdit","tool_input":{"file_path":"b.go","edits":[{"new_string":"chan int"}]}}`, "b.go", "chan int", true},
		{"claude", `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"ls"}}`, "", "", false},
		{"codex", `{"session_id":"s","cwd":"/p","tool_name":"apply_patch","tool_input":{"command":"*** Begin Patch\n*** Update File: /p/w.go\n+errgroup.WithContext(ctx)\n*** End Patch"}}`, "w.go", "errgroup", true},
		{"codex", `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`, "", "", false},
		{"gemini", `{"session_id":"s","cwd":"/p","tool_name":"replace","tool_input":{"file_path":"/p/c.py","old_string":"x","new_string":"asyncio.gather()"}}`, "c.py", "asyncio", true},
		{"gemini", `{"session_id":"s","tool_name":"read_file","tool_input":{"file_path":"c.py"}}`, "", "", false},
	}
	for _, c := range cases {
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		t.Setenv("GEMINI_PROJECT_DIR", "")
		ad, _ := Get(c.agent)
		ev, ok := ad.Parse(BeforeEdit, []byte(c.raw))
		if ok != c.relevant {
			t.Errorf("%s %s: relevant=%v want %v", c.agent, c.raw, ok, c.relevant)
			continue
		}
		if !ok {
			continue
		}
		if len(ev.Files) == 0 || ev.Files[0] != c.wantFile || !strings.Contains(ev.Code, c.wantCode) {
			t.Errorf("%s: files=%v code=%q", c.agent, ev.Files, ev.Code)
		}
	}
}

func TestOutputShapes(t *testing.T) {
	var m map[string]any
	c, _ := Get("claude")
	json.Unmarshal(c.Deny("r"), &m)
	if m["hookSpecificOutput"].(map[string]any)["permissionDecision"] != "deny" {
		t.Fatal("claude deny shape")
	}
	x, _ := Get("codex")
	if string(x.Deny("r")) != string(c.Deny("r")) {
		t.Fatal("codex should mirror claude's PreToolUse output")
	}
	g, _ := Get("gemini")
	m = nil
	json.Unmarshal(g.Deny("r"), &m)
	if m["decision"] != "deny" || m["reason"] != "r" {
		t.Fatal("gemini deny shape")
	}
	if string(g.Noop()) != "{}" || c.Noop() != nil {
		t.Fatal("noop shapes")
	}
}
