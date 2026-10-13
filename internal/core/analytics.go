package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Leitner review intervals in days, by box. A miss sends a concept back to box 0.
var reviewDays = []float64{1, 3, 7, 14, 30, 60}

// ConceptStat tracks one concept tag (e.g. "cancellation") within a topic.
type ConceptStat struct {
	Topic    string  `json:"topic"`
	Concept  string  `json:"concept"`
	Got      int     `json:"got"`
	Missed   int     `json:"missed"`
	Accuracy float64 `json:"accuracy"` // over the last 5 outcomes
	Box      int     `json:"box"`
	LastTS   float64 `json:"last_ts"`
	DueTS    float64 `json:"due_ts"`
	Due      bool    `json:"due"`
}

// TopicStat summarises a topic.
type TopicStat struct {
	ID      string     `json:"id"`
	Done    int        `json:"done"`
	Nailed  int        `json:"nailed"`
	Partial int        `json:"partial"`
	Missed  int        `json:"missed"`
	Score   float64    `json:"score"` // last 10: nailed=1, partial=0.5
	Trend   string     `json:"trend"` // up | down | flat | "" (not enough data)
	LastTS  float64    `json:"last_ts"`
	Recent  []LogEntry `json:"recent"` // newest first, up to 5
}

// Summary is everything the status line, progress file and dashboard show.
type Summary struct {
	Generated float64       `json:"generated"`
	Done      int           `json:"done"`
	Skips     int           `json:"skips"`
	Topics    []TopicStat   `json:"topics"`
	Concepts  []ConceptStat `json:"concepts"`
	Weak      []ConceptStat `json:"weak"` // weakest concepts first
	Due       []ConceptStat `json:"due"`  // due for review, most overdue first
}

// NormalizeTag turns "Channel Ownership" into "channel-ownership".
func NormalizeTag(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.Join(strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '_' || r == '/' }), "-")
	return t
}

// SplitTags parses a comma-separated tag list.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		if t := NormalizeTag(part); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// Summarize computes stats from the log.
func Summarize(entries []LogEntry, now float64) Summary {
	sum := Summary{Generated: now}
	byTopic := map[string][]LogEntry{}
	type key struct{ topic, concept string }
	type hist struct {
		outcomes []bool
		box      int
		last     float64
	}
	concepts := map[key]*hist{}
	for _, e := range entries {
		switch e.Event {
		case "skip":
			sum.Skips++
			continue
		case "done":
		default:
			continue
		}
		sum.Done++
		byTopic[e.Topic] = append(byTopic[e.Topic], e)
		record := func(tag string, ok bool) {
			k := key{e.Topic, tag}
			h := concepts[k]
			if h == nil {
				h = &hist{}
				concepts[k] = h
			}
			h.outcomes = append(h.outcomes, ok)
			h.last = e.TS
			if ok {
				if h.box < len(reviewDays)-1 {
					h.box++
				}
			} else {
				h.box = 0
			}
		}
		for _, t := range e.Got {
			record(t, true)
		}
		for _, t := range e.Missed {
			record(t, false)
		}
	}

	for id, es := range byTopic {
		ts := TopicStat{ID: id, Done: len(es)}
		vals := make([]float64, len(es))
		for i, e := range es {
			switch e.Result {
			case "nailed":
				ts.Nailed++
				vals[i] = 1
			case "partial":
				ts.Partial++
				vals[i] = 0.5
			case "missed":
				ts.Missed++
			}
			ts.LastTS = e.TS
		}
		ts.Score = mean(tail(vals, 10))
		if len(vals) >= 4 {
			half := len(vals) / 2
			if half > 5 {
				half = 5
			}
			recent, before := mean(vals[len(vals)-half:]), mean(vals[len(vals)-2*half:len(vals)-half])
			switch {
			case recent-before > 0.1:
				ts.Trend = "up"
			case before-recent > 0.1:
				ts.Trend = "down"
			default:
				ts.Trend = "flat"
			}
		}
		for i := len(es) - 1; i >= 0 && len(ts.Recent) < 5; i-- {
			ts.Recent = append(ts.Recent, es[i])
		}
		sum.Topics = append(sum.Topics, ts)
	}
	sort.Slice(sum.Topics, func(i, j int) bool { return sum.Topics[i].ID < sum.Topics[j].ID })

	for k, h := range concepts {
		cs := ConceptStat{Topic: k.topic, Concept: k.concept, Box: h.box, LastTS: h.last}
		var recent []float64
		for _, ok := range h.outcomes {
			if ok {
				cs.Got++
			} else {
				cs.Missed++
			}
		}
		for _, ok := range tailBools(h.outcomes, 5) {
			if ok {
				recent = append(recent, 1)
			} else {
				recent = append(recent, 0)
			}
		}
		cs.Accuracy = mean(recent)
		cs.DueTS = h.last + reviewDays[h.box]*86400
		// Only concepts you've struggled with come up for review.
		cs.Due = cs.Missed > 0 && now >= cs.DueTS
		sum.Concepts = append(sum.Concepts, cs)
	}
	sort.Slice(sum.Concepts, func(i, j int) bool {
		a, b := sum.Concepts[i], sum.Concepts[j]
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		return a.Concept < b.Concept
	})
	for _, c := range sum.Concepts {
		if c.Missed > 0 && c.Accuracy < 0.75 {
			sum.Weak = append(sum.Weak, c)
		}
		if c.Due {
			sum.Due = append(sum.Due, c)
		}
	}
	sort.SliceStable(sum.Weak, func(i, j int) bool {
		if sum.Weak[i].Accuracy != sum.Weak[j].Accuracy {
			return sum.Weak[i].Accuracy < sum.Weak[j].Accuracy
		}
		return sum.Weak[i].Missed > sum.Weak[j].Missed
	})
	sort.SliceStable(sum.Due, func(i, j int) bool { return sum.Due[i].DueTS < sum.Due[j].DueTS })
	return sum
}

// KnownTags returns the topic's concept vocabulary: configured seeds first,
// then tags already used in the log, so the agent keeps tagging consistently.
func KnownTags(t *Topic, sum Summary) []string {
	seen := map[string]bool{}
	var out []string
	add := func(tag string) {
		if tag = NormalizeTag(tag); tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	for _, c := range t.Concepts {
		add(c)
	}
	for _, c := range sum.Concepts {
		if c.Topic == t.ID {
			add(c.Concept)
		}
	}
	return out
}

// DueFor lists due concepts for one topic.
func DueFor(topic string, sum Summary) []string {
	var out []string
	for _, c := range sum.Due {
		if c.Topic == topic {
			out = append(out, c.Concept)
		}
	}
	return out
}

// TopicByID finds a topic's stats.
func (s Summary) TopicByID(id string) (TopicStat, bool) {
	for _, t := range s.Topics {
		if t.ID == id {
			return t, true
		}
	}
	return TopicStat{}, false
}

// WriteProgress renders ~/.git-prompted/progress.md.
func WriteProgress() (string, error) {
	sum := Summarize(ReadLog(), float64(time.Now().Unix()))
	var b strings.Builder
	fmt.Fprintf(&b, "# git prompted: progress\n\n_Updated %s. %d checkpoints completed, %d skipped._\n\n",
		time.Now().Format("2006-01-02 15:04"), sum.Done, sum.Skips)
	if sum.Done == 0 {
		b.WriteString("No completed checkpoints yet.\n")
	}
	if len(sum.Weak) > 0 {
		b.WriteString("## Weak spots\n\n| Topic | Concept | Accuracy (last 5) | Missed |\n|---|---|---|---|\n")
		for _, c := range sum.Weak {
			fmt.Fprintf(&b, "| %s | %s | %.0f%% | %d |\n", c.Topic, c.Concept, 100*c.Accuracy, c.Missed)
		}
		b.WriteString("\n")
	}
	if len(sum.Due) > 0 {
		b.WriteString("## Due for review\n\n")
		for _, c := range sum.Due {
			fmt.Fprintf(&b, "- **%s** (%s), last seen %s\n", c.Concept, c.Topic, time.Unix(int64(c.LastTS), 0).Format("2006-01-02"))
		}
		b.WriteString("\n")
	}
	arrows := map[string]string{"up": " ↑", "down": " ↓", "flat": " →"}
	for _, t := range sum.Topics {
		fmt.Fprintf(&b, "## %s: %.0f%%%s\nNailed %d / partial %d / missed %d\n\n", t.ID, 100*t.Score, arrows[t.Trend], t.Nailed, t.Partial, t.Missed)
		for _, e := range t.Recent {
			line := fmt.Sprintf("- %s **%s**", time.Unix(int64(e.TS), 0).Format("2006-01-02"), e.Result)
			if ctx := strings.Trim(e.Project+", "+e.Agent, ", "); ctx != "" {
				line += " (" + ctx + ")"
			}
			if len(e.Missed) > 0 {
				line += " missed: " + strings.Join(e.Missed, ", ") + "."
			}
			if e.Note != "" {
				line += " " + e.Note
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	path := filepath.Join(Home(), "progress.md")
	if err := os.MkdirAll(Home(), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(b.String()), 0o644)
}

// StatusLine renders the compact one-liner for Claude Code's status line.
func StatusLine(cfg *Config, sum Summary, sess *Session, plain bool) string {
	icon, up, down, flat := "🧠", "↑", "↓", ""
	if plain {
		icon, up, down = "gp:", "+", "-"
	}
	if sess != nil && sess.Mode == "off" {
		return icon + " paused"
	}
	var parts []string
	stats := append([]TopicStat(nil), sum.Topics...)
	sort.SliceStable(stats, func(i, j int) bool { return stats[i].Score < stats[j].Score }) // weakest first
	for _, t := range stats {
		if cfg != nil && cfg.Topic(t.ID) == nil {
			continue // topic no longer configured
		}
		arrow := map[string]string{"up": up, "down": down, "flat": flat}[t.Trend]
		parts = append(parts, fmt.Sprintf("%s %.0f%%%s", t.ID, 100*t.Score, arrow))
		if len(parts) == 3 {
			break
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no reps yet")
	}
	if n := len(sum.Due); n > 0 {
		parts = append(parts, fmt.Sprintf("%d due", n))
	}
	return icon + " " + strings.Join(parts, " · ")
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func tail(v []float64, n int) []float64 {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}

func tailBools(v []bool, n int) []bool {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}
