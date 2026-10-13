package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ryangovender/git-prompted/internal/core"
)

func printStats(sum core.Summary) {
	if sum.Done == 0 {
		fmt.Println("No completed checkpoints yet.")
		return
	}
	arrows := map[string]string{"up": "↑", "down": "↓", "flat": "→"}
	fmt.Printf("%d checkpoints completed, %d skipped\n\nTopics\n", sum.Done, sum.Skips)
	for _, t := range sum.Topics {
		fmt.Printf("  %-18s %4.0f%% %s  (%d nailed / %d partial / %d missed)\n",
			t.ID, 100*t.Score, arrows[t.Trend], t.Nailed, t.Partial, t.Missed)
	}
	if len(sum.Weak) > 0 {
		fmt.Println("\nWeak spots")
		for _, c := range sum.Weak {
			fmt.Printf("  %-24s %-14s %3.0f%% (missed %d)\n", c.Concept, c.Topic, 100*c.Accuracy, c.Missed)
		}
	}
	if len(sum.Due) > 0 {
		fmt.Println("\nDue for review")
		for _, c := range sum.Due {
			fmt.Printf("  %-24s %s\n", c.Concept, c.Topic)
		}
	}
}

// statusLine prints one line for Claude Code's status line. It reads Claude's
// status JSON on stdin (for the session and directory) and can wrap another
// status line command so users don't have to choose.
func statusLine(in io.Reader, out io.Writer, plain bool, with string) error {
	raw, _ := io.ReadAll(in)
	var p struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
		Workspace struct {
			CurrentDir string `json:"current_dir"`
			ProjectDir string `json:"project_dir"`
		} `json:"workspace"`
	}
	_ = json.Unmarshal(raw, &p)
	dir := p.Workspace.ProjectDir
	for _, d := range []string{p.Workspace.CurrentDir, p.Cwd} {
		if dir == "" {
			dir = d
		}
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}

	var parts []string
	if with != "" {
		cmd := exec.Command("sh", "-c", with)
		cmd.Stdin = bytes.NewReader(raw)
		cmd.Stderr = os.Stderr
		if o, err := cmd.Output(); err == nil {
			if line := strings.TrimSpace(string(o)); line != "" {
				parts = append(parts, line)
			}
		}
	}
	if cfg := core.LoadConfig(dir, func(string) {}); cfg != nil {
		st := core.ReadState()
		var sess *core.Session
		if p.SessionID != "" {
			sess = st.Sessions[p.SessionID]
		}
		sum := core.Summarize(core.ReadLog(), float64(time.Now().Unix()))
		parts = append(parts, core.StatusLine(cfg, sum, sess, plain))
	}
	if len(parts) > 0 {
		fmt.Fprintln(out, strings.Join(parts, "  │  "))
	}
	return nil
}

func claudeSettingsPath() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "settings.json")
}

// installStatusLine sets Claude Code's statusLine to git-prompted, wrapping any
// existing status line (restored exactly on uninstall).
func installStatusLine(install bool, cmdPath string) error {
	path := claudeSettingsPath()
	orig := filepath.Join(core.Home(), "statusline.orig.json")
	doc := map[string]any{}
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
		backup := fmt.Sprintf("%s.bak-%d", path, time.Now().Unix())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return err
		}
	}
	current, _ := doc["statusLine"].(map[string]any)
	ours := func(m map[string]any) bool {
		c, _ := m["command"].(string)
		return strings.Contains(c, "git-prompted") && strings.Contains(c, "statusline")
	}

	if install {
		if current != nil && ours(current) {
			fmt.Println("Status line already installed.")
			return nil
		}
		command := quoteCmd(cmdPath) + " statusline"
		if current != nil {
			prev, _ := json.Marshal(current)
			if err := os.MkdirAll(core.Home(), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(orig, prev, 0o644); err != nil {
				return err
			}
			if c, _ := current["command"].(string); c != "" {
				command += " --with " + shellQuote(c)
			}
		}
		doc["statusLine"] = map[string]any{"type": "command", "command": command, "padding": 0}
	} else {
		if current == nil || !ours(current) {
			fmt.Println("git prompted status line is not installed.")
			return nil
		}
		delete(doc, "statusLine")
		if prev, err := os.ReadFile(orig); err == nil {
			var m map[string]any
			if json.Unmarshal(prev, &m) == nil {
				doc["statusLine"] = m
			}
			os.Remove(orig)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	outRaw, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, append(outRaw, '\n'), 0o644); err != nil {
		return err
	}
	if install {
		fmt.Printf("Status line installed in %s (your previous one, if any, is kept alongside).\n", path)
	} else {
		fmt.Printf("Status line removed from %s; previous one restored.\n", path)
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
