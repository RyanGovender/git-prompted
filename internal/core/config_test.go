package core

import (
	"os"
	"testing"
)

func TestProseEditsDoNotMatchTopicsWithoutPaths(t *testing.T) {
	tempHome(t)
	os.WriteFile(UserConfigPath(), []byte(`{"topics":[
	  {"id":"errs","patterns":["%w"]},
	  {"id":"docs","paths":["docs/**"],"patterns":["%w"]}]}`), 0o644)
	cfg := LoadConfig(t.TempDir(), func(string) {})
	errs, docs := cfg.Topic("errs"), cfg.Topic("docs")
	code := `fmt.Errorf("x: %w", err)`
	if errs.Matches([]string{"docs/spec.md"}, code) || errs.Matches([]string{"README.MD", "notes.txt"}, code) {
		t.Fatal("prose-only edit matched a topic without paths")
	}
	if !errs.Matches([]string{"w.go"}, code) || !errs.Matches([]string{"docs/spec.md", "w.go"}, code) {
		t.Fatal("code edits must still match")
	}
	if !errs.Matches(nil, code) {
		t.Fatal("edits with unknown files must still match")
	}
	if !docs.Matches([]string{"docs/spec.md"}, code) {
		t.Fatal("a topic that targets docs/** by path must still match prose")
	}
}
