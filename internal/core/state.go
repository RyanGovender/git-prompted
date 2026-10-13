package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session is per agent session (each CLI passes its own session id).
type Session struct {
	Agent       string   `json:"agent,omitempty"`
	Mode        string   `json:"mode"` // on | off
	Explicit    bool     `json:"explicit"`
	Checkpoints int      `json:"checkpoints"`
	PendingQuiz []string `json:"pending_quiz,omitempty"`
	Updated     float64  `json:"updated"`
}

// State is shared by every agent, so progress follows the user across tools.
type State struct {
	Sessions       map[string]*Session `json:"sessions"`
	Cooldowns      map[string]float64  `json:"cooldowns"`
	Muted          []string            `json:"muted"`
	CurrentSession string              `json:"current_session,omitempty"`
}

// Session returns (creating if needed) the session and marks it current.
func (s *State) Session(id, agent, startPrompt string, now float64) *Session {
	sess, ok := s.Sessions[id]
	if !ok {
		mode := "on" // "ask" behaves as on until answered; every checkpoint also offers "not this session"
		if startPrompt == "off" {
			mode = "off"
		}
		sess = &Session{Mode: mode}
		s.Sessions[id] = sess
	}
	if agent != "" {
		sess.Agent = agent
	}
	sess.Updated = now
	s.CurrentSession = id
	return sess
}

// IsMuted reports whether the user muted a topic.
func (s *State) IsMuted(id string) bool {
	for _, m := range s.Muted {
		if m == id {
			return true
		}
	}
	return false
}

// Mute / Unmute a topic.
func (s *State) Mute(id string) {
	if !s.IsMuted(id) {
		s.Muted = append(s.Muted, id)
	}
}
func (s *State) Unmute(id string) {
	out := s.Muted[:0]
	for _, m := range s.Muted {
		if m != id {
			out = append(out, m)
		}
	}
	s.Muted = out
}

// AddPending queues a quiz-after topic once.
func (sess *Session) AddPending(id string) {
	for _, p := range sess.PendingQuiz {
		if p == id {
			return
		}
	}
	sess.PendingQuiz = append(sess.PendingQuiz, id)
}

func statePath() string { return filepath.Join(Home(), "state.json") }

// WithState runs fn on the state under a file lock and saves the result.
// Hooks can run in parallel (several sessions, several agents), hence the lock.
func WithState(fn func(*State) error) error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	unlock, err := lock(filepath.Join(Home(), "state.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	s := ReadState()
	if err := fn(s); err != nil {
		return err
	}
	cutoff := float64(time.Now().Add(-7 * 24 * time.Hour).Unix())
	for id, sess := range s.Sessions {
		if sess.Updated < cutoff {
			delete(s.Sessions, id)
		}
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath())
}

// ReadState loads state without locking (read-only callers).
func ReadState() *State {
	s := &State{}
	if raw, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(raw, s)
	}
	if s.Sessions == nil {
		s.Sessions = map[string]*Session{}
	}
	if s.Cooldowns == nil {
		s.Cooldowns = map[string]float64{}
	}
	if s.Muted == nil {
		s.Muted = []string{}
	}
	return s
}

func lock(path string) (func(), error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 5*time.Second {
			os.Remove(path) // stale lock from a killed process
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// LogEntry is one line of ~/.git-prompted/log.jsonl.
type LogEntry struct {
	Event   string   `json:"event"` // done | skip | session-off | mute
	Topic   string   `json:"topic,omitempty"`
	Result  string   `json:"result,omitempty"`
	Got     []string `json:"got,omitempty"`    // concept tags the user got right
	Missed  []string `json:"missed,omitempty"` // concept tags the user missed
	Note    string   `json:"note,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Project string   `json:"project,omitempty"`
	Session string   `json:"session,omitempty"`
	TS      float64  `json:"ts"`
}

// AppendLog records an outcome.
func AppendLog(e LogEntry) error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(Home(), "log.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, _ := json.Marshal(e)
	_, err = f.Write(append(raw, '\n'))
	return err
}

// ReadLog returns all logged entries.
func ReadLog() []LogEntry {
	raw, err := os.ReadFile(filepath.Join(Home(), "log.jsonl"))
	if err != nil {
		return nil
	}
	var out []LogEntry
	for _, line := range strings.Split(string(raw), "\n") {
		var e LogEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.Event != "" {
			out = append(out, e)
		}
	}
	return out
}
