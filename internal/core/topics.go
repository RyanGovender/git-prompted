package core

import (
	"bytes"
	"encoding/json"
	"fmt"
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
