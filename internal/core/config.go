package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Topic is one thing the user wants to keep practising.
type Topic struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Mode     string   `json:"mode,omitempty"` // "ask-first" (default; legacy alias "think-first") or "quiz-after"
	Level    string   `json:"level,omitempty"`
	Goals    []string `json:"goals,omitempty"`
	Patterns []string `json:"patterns,omitempty"` // RE2 regexes matched against the code being written
	Paths    []string `json:"paths,omitempty"`    // globs (supports **) matched against project-relative paths
	Concepts []string `json:"concepts,omitempty"` // seed vocabulary of concept tags for grading

	patterns []*regexp.Regexp
	paths    []*regexp.Regexp
}

// DisplayName is the human-readable topic name.
func (t *Topic) DisplayName() string {
	if t.Name != "" {
		return t.Name
	}
	return t.ID
}

// QuizAfter reports whether the topic quizzes after the turn instead of pausing before edits.
func (t *Topic) QuizAfter() bool { return t.Mode == "quiz-after" }

// Matches reports whether a change touching files with the given code exercises this topic.
func (t *Topic) Matches(files []string, code string) bool {
	if len(t.paths) > 0 {
		hit := false
		for _, f := range files {
			for _, g := range t.paths {
				if g.MatchString(f) {
					hit = true
				}
			}
		}
		if !hit {
			return false
		}
	}
	if len(t.patterns) == 0 {
		return len(t.paths) > 0 // path-only topic
	}
	for _, re := range t.patterns {
		if re.MatchString(code) {
			return true
		}
	}
	return false
}

// Config is the merged user + project configuration.
type Config struct {
	StartPrompt     string   `json:"start_prompt,omitempty"` // ask | on | off
	CooldownMinutes *float64 `json:"cooldown_minutes,omitempty"`
	MaxCheckpoints  *int     `json:"max_checkpoints_per_session,omitempty"`
	Topics          []*Topic `json:"topics,omitempty"`
}

// Cooldown is how long a topic stays quiet after a checkpoint, skip or quiz.
func (c *Config) Cooldown() time.Duration {
	if c.CooldownMinutes == nil {
		return 45 * time.Minute
	}
	return time.Duration(*c.CooldownMinutes * float64(time.Minute))
}

// MaxPerSession caps ask-first checkpoints per session.
func (c *Config) MaxPerSession() int {
	if c.MaxCheckpoints == nil {
		return 3
	}
	return *c.MaxCheckpoints
}

// Topic looks up a topic by id.
func (c *Config) Topic(id string) *Topic {
	for _, t := range c.Topics {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Home is where config, state and progress live (shared by every agent).
func Home() string {
	if h := os.Getenv("GIT_PROMPTED_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".git-prompted"
	}
	return filepath.Join(home, ".git-prompted")
}

// UserConfigPath is the personal goals file.
func UserConfigPath() string { return filepath.Join(Home(), "goals.json") }

// ProjectConfigPath is the optional, committable per-project goals file.
func ProjectConfigPath(projectDir string) string {
	return filepath.Join(projectDir, ".git-prompted.json")
}

// LoadConfig merges the user config with an optional project config.
// Project scalars win; topics merge by id. Returns nil when there are no topics
// (the plugin then stays completely inert). Problems are reported via warn and skipped.
func LoadConfig(projectDir string, warn func(string)) *Config {
	cfg := &Config{StartPrompt: "ask"}
	byID := map[string]*Topic{}
	var order []string
	for _, path := range []string{UserConfigPath(), ProjectConfigPath(projectDir)} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var c Config
		if err := json.Unmarshal(raw, &c); err != nil {
			warn(fmt.Sprintf("ignoring invalid config %s: %v", path, err))
			continue
		}
		if c.StartPrompt != "" {
			cfg.StartPrompt = c.StartPrompt
		}
		if c.CooldownMinutes != nil {
			cfg.CooldownMinutes = c.CooldownMinutes
		}
		if c.MaxCheckpoints != nil {
			cfg.MaxCheckpoints = c.MaxCheckpoints
		}
		for _, t := range c.Topics {
			if t == nil || t.ID == "" {
				continue
			}
			if _, seen := byID[t.ID]; !seen {
				order = append(order, t.ID)
			}
			byID[t.ID] = t
		}
	}
	for _, id := range order {
		t := byID[id]
		for _, p := range t.Patterns {
			re, err := regexp.Compile("(?m)" + p)
			if err != nil {
				warn(fmt.Sprintf("topic %s: bad pattern %q: %v", t.ID, p, err))
				continue
			}
			t.patterns = append(t.patterns, re)
		}
		for _, g := range t.Paths {
			t.paths = append(t.paths, GlobToRegexp(g))
		}
		cfg.Topics = append(cfg.Topics, t)
	}
	if len(cfg.Topics) == 0 {
		return nil
	}
	return cfg
}

// GlobToRegexp converts a glob with ** support into an anchored regexp.
func GlobToRegexp(glob string) *regexp.Regexp {
	glob = filepath.ToSlash(glob)
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
