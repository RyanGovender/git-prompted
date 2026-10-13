package core

import (
	"strings"
	"testing"
)

const day = 86400.0

func done(topic, result string, ts float64, got, missed []string) LogEntry {
	return LogEntry{Event: "done", Topic: topic, Result: result, TS: ts, Got: got, Missed: missed}
}

func TestSplitTags(t *testing.T) {
	got := SplitTags(" Channel Ownership, cancellation,,cancellation, data_race ")
	want := "channel-ownership,cancellation,data-race"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %v", got)
	}
}

func TestLeitnerDueAndWeakSpots(t *testing.T) {
	log := []LogEntry{
		done("concurrency", "partial", 0, []string{"channel-ownership"}, []string{"cancellation"}),
		done("concurrency", "missed", 1*day, nil, []string{"cancellation", "goroutine-leak"}),
		done("concurrency", "nailed", 2*day, []string{"goroutine-leak"}, nil),
		{Event: "skip", Topic: "concurrency", TS: 2 * day},
	}
	// Day 2.5: cancellation (box 0, last seen day 1) is due after 1 day -> due.
	// goroutine-leak was missed then got (box 1 -> 3 days) -> not due yet.
	sum := Summarize(log, 2.5*day)
	if sum.Done != 3 || sum.Skips != 1 {
		t.Fatalf("counts: %+v", sum)
	}
	if len(sum.Due) != 1 || sum.Due[0].Concept != "cancellation" {
		t.Fatalf("due: %+v", sum.Due)
	}
	if len(sum.Weak) == 0 || sum.Weak[0].Concept != "cancellation" || sum.Weak[0].Accuracy != 0 {
		t.Fatalf("weak: %+v", sum.Weak)
	}
	for _, c := range sum.Concepts {
		if c.Concept == "channel-ownership" && c.Due {
			t.Fatal("never-missed concepts are not scheduled for review")
		}
	}
	// A week later goroutine-leak is due too.
	if later := Summarize(log, 9*day); len(later.Due) != 2 {
		t.Fatalf("later due: %+v", later.Due)
	}
}

func TestTopicScoreAndTrend(t *testing.T) {
	var log []LogEntry
	for i, r := range []string{"missed", "missed", "partial", "nailed", "nailed", "nailed"} {
		log = append(log, done("architecture", r, float64(i)*day, nil, nil))
	}
	ts, ok := Summarize(log, 10*day).TopicByID("architecture")
	if !ok || ts.Trend != "up" || ts.Nailed != 3 || len(ts.Recent) != 5 || ts.Recent[0].Result != "nailed" {
		t.Fatalf("topic: %+v", ts)
	}
	if ts.Score < 0.58 || ts.Score > 0.59 { // (0+0+0.5+1+1+1)/6
		t.Fatalf("score %.3f", ts.Score)
	}
}

func TestStatusLine(t *testing.T) {
	cfg := &Config{Topics: []*Topic{{ID: "concurrency"}, {ID: "architecture"}}}
	log := []LogEntry{
		done("concurrency", "missed", 0, nil, []string{"cancellation"}),
		done("architecture", "nailed", 0, []string{"coupling"}, nil),
		done("removed-topic", "nailed", 0, nil, nil),
	}
	line := StatusLine(cfg, Summarize(log, 5*day), nil, false)
	if line != "🧠 concurrency 0% · architecture 100% · 1 due" {
		t.Fatalf("line: %q", line)
	}
	if got := StatusLine(cfg, Summary{}, nil, true); got != "gp: no reps yet" {
		t.Fatalf("empty: %q", got)
	}
	if got := StatusLine(cfg, Summary{}, &Session{Mode: "off"}, false); got != "🧠 paused" {
		t.Fatalf("paused: %q", got)
	}
}

func TestKnownTagsMergesSeedsAndHistory(t *testing.T) {
	topic := &Topic{ID: "concurrency", Concepts: []string{"Cancellation", "data race"}}
	sum := Summarize([]LogEntry{done("concurrency", "partial", 0, []string{"backpressure"}, []string{"cancellation"})}, 0)
	if got := strings.Join(KnownTags(topic, sum), ","); got != "cancellation,data-race,backpressure" {
		t.Fatalf("tags: %s", got)
	}
}
