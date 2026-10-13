package core

import (
	"fmt"
	"strings"
	"time"
)

// Event is a hook call normalised away from any particular agent CLI.
type Event struct {
	Agent          string // claude | codex | gemini
	SessionID      string
	ProjectDir     string
	Source         string   // session start: startup | resume | clear | compact
	Files          []string // project-relative, forward slashes
	Code           string   // the code about to be written (added lines only)
	StopHookActive bool
}

// Engine makes the decisions. It never calls a model: it only decides when to
// pause and what instructions to hand back to the agent the user is already running.
type Engine struct {
	Cfg      *Config
	Exe      string // path of this binary, used in the commands the agent runs
	Coaching string // coaching guide, injected for agents without a skill/context file
	Now      func() time.Time
}

func (e *Engine) now() float64 {
	if e.Now == nil {
		return float64(time.Now().UnixNano()) / 1e9
	}
	return float64(e.Now().UnixNano()) / 1e9
}

// SessionStart returns context to inject at session start ("" for none).
func (e *Engine) SessionStart(ev Event) (string, error) {
	var out string
	err := WithState(func(s *State) error {
		sess := s.Session(ev.SessionID, ev.Agent, e.Cfg.StartPrompt, e.now())
		if sess.Mode == "off" && sess.Explicit {
			return nil
		}
		var names []string
		for _, t := range e.Cfg.Topics {
			if !s.IsMuted(t.ID) {
				names = append(names, t.DisplayName())
			}
		}
		if len(names) == 0 {
			return nil
		}
		list := strings.Join(names, ", ")
		cli := e.cli(ev.SessionID)
		var b strings.Builder
		fmt.Fprintf(&b, "[git-prompted] Learning checkpoints are installed. The user wants to keep practising these topics "+
			"instead of always having them done for them: %s. Command for recording their choices: %s <command>. ", list, cli)
		if ev.Agent == "claude" {
			b.WriteString("Follow the git-prompted coaching skill whenever a checkpoint or quiz comes up.")
		} else if e.Coaching != "" && ev.Agent == "codex" {
			b.WriteString("Follow this coaching guide whenever a checkpoint or quiz comes up:\n\n" + e.Coaching + "\n")
		} else {
			b.WriteString("Follow the git-prompted coaching guide whenever a checkpoint or quiz comes up.")
		}
		fresh := ev.Source == "" || ev.Source == "startup" || ev.Source == "clear"
		if e.Cfg.StartPrompt == "ask" && !sess.Explicit && fresh {
			fmt.Fprintf(&b, "\nBefore handling the user's first request, ask once %s: \"Learning checkpoints on for this session? (%s)\" "+
				"with options On / Off. Then run `%s session on` or `%s session off` and continue with their request. "+
				"Keep it to one line; do not lecture.", askHint(ev.Agent), list, cli, cli)
		}
		out = b.String()
		return nil
	})
	return out, err
}

// BeforeEdit returns a denial reason when a git-prompted checkpoint should
// interrupt this edit, or "" to let it through. Quiz-after topics are queued.
func (e *Engine) BeforeEdit(ev Event) (string, error) {
	var reason string
	err := WithState(func(s *State) error {
		sess := s.Session(ev.SessionID, ev.Agent, e.Cfg.StartPrompt, e.now())
		if sess.Mode == "off" {
			return nil
		}
		now, cooldown := e.now(), e.Cfg.Cooldown().Seconds()
		var checkpoint *Topic
		var quizzes []string
		for _, t := range e.Cfg.Topics {
			if s.IsMuted(t.ID) || now-s.Cooldowns[t.ID] < cooldown || !t.Matches(ev.Files, ev.Code) {
				continue
			}
			if t.QuizAfter() {
				quizzes = append(quizzes, t.ID)
				continue
			}
			if checkpoint == nil && sess.Checkpoints < e.Cfg.MaxPerSession() {
				checkpoint = t
			}
		}
		if checkpoint != nil {
			// The edit is blocked, so nothing is written yet; quizzes queue when it is retried.
			sess.Checkpoints++
			reason = e.checkpointPrompt(checkpoint, ev)
			return nil
		}
		for _, id := range quizzes {
			sess.AddPending(id)
		}
		return nil
	})
	return reason, err
}

// AfterTurn returns a continuation prompt offering a quiz, or "" to finish normally.
func (e *Engine) AfterTurn(ev Event) (string, error) {
	if ev.StopHookActive {
		return "", nil // never loop
	}
	var reason string
	err := WithState(func(s *State) error {
		sess, ok := s.Sessions[ev.SessionID]
		if !ok || sess.Mode == "off" || len(sess.PendingQuiz) == 0 {
			return nil
		}
		var topics []*Topic
		for _, id := range sess.PendingQuiz {
			if t := e.Cfg.Topic(id); t != nil && !s.IsMuted(id) {
				topics = append(topics, t)
			}
		}
		sess.PendingQuiz = nil
		now := e.now()
		sess.Updated = now
		for _, t := range topics {
			s.Cooldowns[t.ID] = now // one quiz per topic per cooldown window
		}
		if len(topics) > 0 {
			reason = e.quizPrompt(topics, ev)
		}
		return nil
	})
	return reason, err
}

func (e *Engine) summary() Summary { return Summarize(ReadLog(), e.now()) }

// gradingGuide tells the agent which concept tags to use and what's due for review.
func (e *Engine) gradingGuide(t *Topic, sum Summary) string {
	var b strings.Builder
	if tags := KnownTags(t, sum); len(tags) > 0 {
		fmt.Fprintf(&b, "\nConcept tags for %s (reuse these where they fit; add a new short kebab-case tag only if none fits): %s.", t.ID, strings.Join(tags, ", "))
	}
	if due := DueFor(t.ID, sum); len(due) > 0 {
		fmt.Fprintf(&b, "\nDue for review (the user missed these before): %s. If one is relevant to this change, make your question about it.", strings.Join(due, ", "))
	}
	return b.String()
}

func (e *Engine) doneCmd(cli, topic string) string {
	return fmt.Sprintf(`%s done --topic %s --result <nailed|partial|missed> --got <tags they got right> --missed <tags they missed> --note "<one line>"`, cli, topic)
}

func (e *Engine) cli(session string) string {
	return fmt.Sprintf("%s --session %s", quote(e.Exe), session)
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t'\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

func askHint(agent string) string {
	if agent == "claude" {
		return "(use the AskUserQuestion tool if you have it, otherwise a short numbered list)"
	}
	return "(as a short numbered list, then stop and wait for their answer)"
}

func (e *Engine) checkpointPrompt(t *Topic, ev Event) string {
	cli := e.cli(ev.SessionID)
	goals := strings.Join(t.Goals, "; ")
	if goals == "" {
		goals = "build real understanding of this topic"
	}
	where := "this change"
	if len(ev.Files) > 0 {
		where = strings.Join(ev.Files, ", ")
	}
	level := t.Level
	if level == "" {
		level = "unspecified"
	}
	name := t.DisplayName()
	return fmt.Sprintf(`[git-prompted] Learning checkpoint: %[1]s (in %[2]s).
The user chose to practise this topic. Their goals: %[3]s. Level: %[4]s.%[8]s
Do NOT write this code yet. First ask the user %[5]s:
  "This touches %[1]s, one of your learning goals. Want to think it through first?"
Options and what to do:
  1. Yes, let me think -> follow the git-prompted coaching guide: frame the problem without giving away your solution, ask ONE focused question, wait for the answer, then compare it with your plan honestly. Build on their design where it is sound. Then record it (comma-separated tags; leave a flag out if empty):
     %[9]s
  2. Not this one -> run: %[6]s decide --topic %[7]s --choice skip
  3. Not this session -> run: %[6]s decide --choice session-off
  4. Stop asking about %[1]s -> run: %[6]s decide --topic %[7]s --choice mute
After running the command, retry the edit and continue the task; it will be allowed.`,
		name, where, goals, level, askHint(ev.Agent), cli, t.ID, e.gradingGuide(t, e.summary()), e.doneCmd(cli, t.ID))
}

func (e *Engine) quizPrompt(topics []*Topic, ev Event) string {
	cli := e.cli(ev.SessionID)
	sum := e.summary()
	var names, cmds []string
	var guide strings.Builder
	for _, t := range topics {
		names = append(names, t.DisplayName())
		cmds = append(cmds, "     "+e.doneCmd(cli, t.ID))
		guide.WriteString(e.gradingGuide(t, sum))
	}
	list := strings.Join(names, ", ")
	intro := "Before finishing, give your normal summary, then offer"
	if ev.Agent == "gemini" {
		// Gemini's AfterAgent retry replaces the previous reply.
		intro = "Your previous reply may not be shown, so first restate its key points in two or three lines, then offer"
	}
	return fmt.Sprintf(`[git-prompted] You just wrote code touching the user's learning topic(s): %[1]s.%[6]s
%[2]s a quick quiz %[3]s:
  "Quick 2-question check on %[1]s?" Options: Quiz me / Skip / Not this session.
  Quiz me -> follow the git-prompted coaching guide: 2-3 questions grounded in the code you just wrote, one at a time, grade honestly, then for each topic run (comma-separated tags; leave a flag out if empty):
%[4]s
  Skip -> just finish.
  Not this session -> run: %[5]s decide --choice session-off`,
		list, intro, askHint(ev.Agent), strings.Join(cmds, "\n"), cli, guide.String())
}
