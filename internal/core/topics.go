package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var topicIDRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateTopic is the gate for every topic written to goals.json. LoadConfig on
// the hot path only skips bad patterns, so a broken topic must be rejected here.
// It normalises concept tags in place.
func ValidateTopic(t *Topic) error {
	if t.ID == "" {
		return fmt.Errorf("topic %q: id is required", t.Name)
	}
	if !topicIDRe.MatchString(t.ID) {
		return fmt.Errorf("topic %q: id must be kebab-case, e.g. %q", t.ID, NormalizeTag(t.ID))
	}
	switch t.Mode {
	case "", "ask-first", "think-first", "quiz-after":
	default:
		return fmt.Errorf("topic %s: mode %q must be ask-first or quiz-after", t.ID, t.Mode)
	}
	if len(t.Patterns) == 0 && len(t.Paths) == 0 {
		return fmt.Errorf("topic %s: needs at least one pattern or path", t.ID)
	}
	for i, p := range t.Patterns {
		if _, err := regexp.Compile("(?m)" + p); err != nil {
			return fmt.Errorf("topic %s: pattern %d %q does not compile: %v", t.ID, i+1, p, err)
		}
	}
	t.Concepts = SplitTags(strings.Join(t.Concepts, ","))
	return nil
}

// ParseTopicsJSON reads one topic object or an array of them. Unknown fields are
// errors, so a typo like "pattern" fails loudly instead of saving a topic that never fires.
func ParseTopicsJSON(raw []byte) ([]*Topic, error) {
	raw = bytes.TrimSpace(raw)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var topics []*Topic
	var err error
	if len(raw) > 0 && raw[0] == '[' {
		err = dec.Decode(&topics)
	} else {
		var t Topic
		err = dec.Decode(&t)
		topics = []*Topic{&t}
	}
	if err != nil {
		return nil, fmt.Errorf("invalid topic JSON: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("invalid topic JSON: trailing data after the first value; pass an array for several topics")
	}
	if len(topics) == 0 {
		return nil, fmt.Errorf("no topics in input")
	}
	for _, t := range topics {
		if t == nil {
			return nil, fmt.Errorf("invalid topic JSON: null topic")
		}
	}
	return topics, nil
}

// TopicListing is one row of `git-prompted topics list`.
type TopicListing struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Mode     string   `json:"mode"`
	Level    string   `json:"level,omitempty"`
	Goals    []string `json:"goals,omitempty"`
	Source   string   `json:"source"`   // catalog | custom
	Selected bool     `json:"selected"` // already in goals.json
}

// AddTopics validates every topic, then writes them into goals.json in one step:
// a topic whose id is already there is replaced in place, others are appended.
// Nothing is written if any topic is invalid. AddTopics(nil) creates an empty config.
func AddTopics(topics []*Topic) (added, replaced []string, err error) {
	for _, t := range topics {
		if err := ValidateTopic(t); err != nil {
			return nil, nil, err
		}
	}
	err = editUserConfig(func(mine []*Topic) ([]*Topic, bool) {
		for _, t := range topics {
			if i := indexOf(mine, t.ID); i >= 0 {
				mine[i] = t
				replaced = append(replaced, t.ID)
			} else {
				mine = append(mine, t)
				added = append(added, t.ID)
			}
		}
		return mine, true
	})
	return added, replaced, err
}

// RemoveTopics drops topics from goals.json. The log and stats are kept, so adding
// a topic back restores its history.
func RemoveTopics(ids []string) (removed, missing []string, err error) {
	err = editUserConfig(func(mine []*Topic) ([]*Topic, bool) {
		for _, id := range ids {
			id = NormalizeTag(id)
			if i := indexOf(mine, id); i >= 0 {
				mine = append(mine[:i], mine[i+1:]...)
				removed = append(removed, id)
			} else {
				missing = append(missing, id)
			}
		}
		return mine, len(removed) > 0
	})
	return removed, missing, err
}

// ListTopics lists the catalog, marking entries already in goals.json, followed by
// the user's custom topics.
func ListTopics(catalog []*Topic) ([]TopicListing, error) {
	mine, err := readUserTopics()
	if err != nil {
		return nil, err
	}
	var out []TopicListing
	for _, t := range catalog {
		out = append(out, listing(t, "catalog", indexOf(mine, t.ID) >= 0))
	}
	for _, t := range mine {
		if indexOf(catalog, t.ID) < 0 {
			out = append(out, listing(t, "custom", true))
		}
	}
	return out, nil
}

func listing(t *Topic, source string, selected bool) TopicListing {
	mode := t.Mode
	if mode == "" {
		mode = "ask-first"
	}
	return TopicListing{ID: t.ID, Name: t.DisplayName(), Mode: mode, Level: t.Level, Goals: t.Goals, Source: source, Selected: selected}
}

func indexOf(topics []*Topic, id string) int {
	for i, t := range topics {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// readUserTopics returns the topics in goals.json (none if the file is missing).
func readUserTopics() ([]*Topic, error) {
	raw, err := os.ReadFile(UserConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, topics, err := decodeUserConfig(raw)
	return topics, err
}

func decodeUserConfig(raw []byte) (map[string]json.RawMessage, []*Topic, error) {
	keys := map[string]json.RawMessage{}
	var topics []*Topic
	err := json.Unmarshal(raw, &keys)
	if err == nil && keys["topics"] != nil {
		err = json.Unmarshal(keys["topics"], &topics)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not valid JSON (%v); fix or move it, then retry", UserConfigPath(), err)
	}
	return keys, topics, nil
}

// editUserConfig applies fn to the topics in goals.json under the state lock and
// writes the result atomically, keeping every other key. The previous file is
// copied to goals.json.bak first. fn reports whether anything changed.
func editUserConfig(fn func([]*Topic) ([]*Topic, bool)) error {
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return err
	}
	unlock, err := lock(filepath.Join(Home(), "state.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	path := UserConfigPath()
	keys := map[string]json.RawMessage{}
	var topics []*Topic
	raw, err := os.ReadFile(path)
	exists := err == nil
	switch {
	case exists:
		if keys, topics, err = decodeUserConfig(raw); err != nil {
			return err
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	topics, changed := fn(topics)
	if !changed {
		return nil
	}
	if exists {
		if err := os.WriteFile(path+".bak", raw, 0o644); err != nil {
			return err
		}
	}
	if topics == nil {
		topics = []*Topic{}
	}
	out := map[string]any{}
	for k, v := range keys {
		out[k] = v
	}
	out["topics"] = topics
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep patterns like `ch <- v` readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DefaultTopicIDs are what `init --defaults` sets up (the pre-0.4 example config).
var DefaultTopicIDs = []string{"concurrency", "architecture"}

// ParseCatalog parses and checks the built-in topic catalog (assets/topics.json).
func ParseCatalog(raw []byte) ([]*Topic, error) {
	topics, err := ParseTopicsJSON(raw)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, t := range topics {
		if err := ValidateTopic(t); err != nil {
			return nil, err
		}
		if seen[t.ID] {
			return nil, fmt.Errorf("topic %s listed twice", t.ID)
		}
		seen[t.ID] = true
	}
	return topics, nil
}

// CatalogTopics picks catalog entries by id (normalised, duplicates dropped).
// An unknown id fails with the list of valid ones.
func CatalogTopics(catalog []*Topic, ids []string) ([]*Topic, error) {
	var out []*Topic
	seen := map[string]bool{}
	for _, id := range ids {
		id = NormalizeTag(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		i := indexOf(catalog, id)
		if i < 0 {
			var valid []string
			for _, t := range catalog {
				valid = append(valid, t.ID)
			}
			return nil, fmt.Errorf("no catalog topic %q; choose from: %s (or add a custom topic with --json -)", id, strings.Join(valid, ", "))
		}
		out = append(out, catalog[i])
	}
	return out, nil
}

// NeedsOnboarding reports whether a session start should run topic onboarding: a
// fresh session and no personal goals.json yet. Declining writes an empty goals.json;
// Engine.OnboardingStart also offers it only once, so ignoring it never nags.
func NeedsOnboarding(source string) bool {
	if source != "" && source != "startup" && source != "clear" {
		return false
	}
	_, err := os.Stat(UserConfigPath())
	return errors.Is(err, os.ErrNotExist)
}
