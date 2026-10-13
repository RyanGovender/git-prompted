# Topic Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Users pick their own learning topics inside their agent (built-in catalog or agent-drafted custom topics) instead of `init` silently copying two defaults.

**Architecture:** A validated topic catalog is embedded in the binary. A new `topics` CLI is the only path that writes topics into `~/.git-prompted/goals.json`, and all writes go through `core.ValidateTopic`. When no `goals.json` exists, the session-start hook hands the agent onboarding instructions; the conversation itself is defined in `assets/coaching.md`. Topics without `paths` ignore prose-only edits.

**Tech Stack:** Go standard library only (`encoding/json`, `regexp`, `embed`), POSIX sh for `scripts/sync.sh`.

**Spec:** `docs/superpowers/specs/2026-10-13-topic-onboarding-design.md`

**Deviation from spec:** the spec names `core.Catalog()`. `core` must not depend on `assets`, so the API is `core.ParseCatalog(raw []byte)` and callers pass `assets.Topics`.

## Global Constraints

- No model calls and no network in the binary.
- Standard library only; no new modules in `go.mod`.
- `git-prompted hook ...` always exits 0 and prints a valid (possibly no-op) response.
- Every onboarding prompt offers "Not now"; declining writes `{"topics": []}` and never re-prompts automatically.
- Tests always set `GIT_PROMPTED_HOME` to a temp dir; never touch the real home directory.
- Concept tags go through `SplitTags` / `NormalizeTag`.
- Topic ids are kebab-case (`^[a-z0-9]+(?:-[a-z0-9]+)*$`). Modes: `ask-first` (default), legacy `think-first`, `quiz-after`.
- Patterns are compiled as `regexp.Compile("(?m)" + p)`, same as `LoadConfig`.
- Edit `assets/coaching.md`, `bin/git-prompted`, `VERSION` only at the source, then `make sync`; never hand-edit generated copies.
- Target version: `0.4.0`.
- Run `gofmt -w .` and `make test` before every commit.

## Review Focus

1. `goals.json` exists but is malformed → `topics add/remove` must fail with a clear message and leave the file byte-for-byte untouched (test in Task 2).
2. Custom topic JSON with a misspelled field (`"pattern"`) or wrong type (`"patterns": "x"`) → rejected, not saved as a topic that never fires (test in Task 1).
3. Patterns containing `<`, `>` or `&` (e.g. `ch <- v`) → written to `goals.json` literally, not as `<` (test in Task 2).
4. User removes every topic → file stays as `{"topics": []}`; onboarding does not re-trigger and hooks stay quiet (tests in Task 2 and Task 7).
5. Agent runs the printed form `<cli> --session ID topics add --json -` → `-` is the stdin marker and the command works without a known session (test in Task 5).

---

### Task 1: Topic validation and JSON parsing

**Files:**
- Create: `internal/core/topics.go`
- Test: `internal/core/topics_test.go`

**Interfaces:**
- Consumes: `Topic`, `NormalizeTag`, `SplitTags` (existing in `internal/core`).
- Produces:
  - `func ValidateTopic(t *Topic) error`: normalises `t.Concepts` in place.
  - `func ParseTopicsJSON(raw []byte) ([]*Topic, error)`: accepts one object or an array; rejects unknown fields.

- [ ] **Step 1: Write the failing test**

Create `internal/core/topics_test.go`:

```go
package core

import (
	"strings"
	"testing"
)

func TestValidateTopicRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		`{"id":"Bad ID","patterns":["x"]}`:             `kebab-case, e.g. "bad-id"`,
		`{"id":"a","patterns":["use(State"]}`:          `pattern 1 "use(State" does not compile`,
		`{"id":"a"}`:                                   "at least one pattern or path",
		`{"id":"a","mode":"sometimes","patterns":["x"]}`: "mode",
		`{"patterns":["x"]}`:                           "id is required",
		`{"id":"a","pattern":["x"]}`:                   "invalid topic JSON",
		`{"id":"a","patterns":"x"}`:                    "invalid topic JSON",
		`[]`:                                           "no topics",
	}
	for in, want := range cases {
		ts, err := ParseTopicsJSON([]byte(in))
		if err == nil {
			err = ValidateTopic(ts[0])
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want error containing %q", in, err, want)
		}
	}
}

func TestParseTopicsJSONAcceptsObjectOrArray(t *testing.T) {
	one, err := ParseTopicsJSON([]byte(` {"id":"a","patterns":["x"],"concepts":["Error Wrapping","error-wrapping"]} `))
	if err != nil || len(one) != 1 {
		t.Fatalf("object: %v %v", one, err)
	}
	if err := ValidateTopic(one[0]); err != nil {
		t.Fatal(err)
	}
	if strings.Join(one[0].Concepts, ",") != "error-wrapping" {
		t.Fatalf("concepts not normalised: %v", one[0].Concepts)
	}
	many, err := ParseTopicsJSON([]byte(`[{"id":"a","paths":["docs/**"]},{"id":"b","mode":"quiz-after","patterns":["y"]}]`))
	if err != nil || len(many) != 2 {
		t.Fatalf("array: %v %v", many, err)
	}
	for _, tp := range many {
		if err := ValidateTopic(tp); err != nil {
			t.Fatal(err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'TestValidateTopic|TestParseTopicsJSON' -v`
Expected: FAIL to compile with `undefined: ParseTopicsJSON` / `undefined: ValidateTopic`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/core/topics.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/core && go test ./internal/core/ -run 'TestValidateTopic|TestParseTopicsJSON' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/topics.go internal/core/topics_test.go
git commit -m "Add topic validation and JSON parsing for goals.json writes"
```

---

### Task 2: Editing goals.json (add, remove, list)

**Files:**
- Modify: `internal/core/topics.go`
- Test: `internal/core/topics_test.go`

**Interfaces:**
- Consumes: `ValidateTopic`, `UserConfigPath`, `Home`, unexported `lock(path string) (func(), error)` from `state.go`.
- Produces:
  - `type TopicListing struct { ID, Name, Mode, Level string; Goals []string; Source string; Selected bool }` (JSON: `id,name,mode,level,goals,source,selected`)
  - `func AddTopics(topics []*Topic) (added, replaced []string, err error)`: `AddTopics(nil)` creates `{"topics": []}` when the file is missing.
  - `func RemoveTopics(ids []string) (removed, missing []string, err error)`
  - `func ListTopics(catalog []*Topic) ([]TopicListing, error)`
  - unexported `indexOf(topics []*Topic, id string) int`, `decodeUserConfig(raw []byte) (map[string]json.RawMessage, []*Topic, error)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/core/topics_test.go` (add `"bytes"`, `"encoding/json"`, `"os"` to its imports):

```go
func tempHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	t.Setenv("GIT_PROMPTED_HOME", h)
	return h
}

func readGoals(t *testing.T) (map[string]json.RawMessage, []*Topic, []byte) {
	t.Helper()
	raw, err := os.ReadFile(UserConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	keys, topics, err := decodeUserConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return keys, topics, raw
}

func TestAddTopicsReplacesInPlaceAndKeepsOtherKeys(t *testing.T) {
	tempHome(t)
	orig := []byte(`{"start_prompt":"on","cooldown_minutes":10,"topics":[{"id":"a","patterns":["A"]},{"id":"b","patterns":["B"]}]}`)
	os.WriteFile(UserConfigPath(), orig, 0o644)

	added, replaced, err := AddTopics([]*Topic{{ID: "a", Patterns: []string{"AA"}}, {ID: "c", Patterns: []string{"ch <- v"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(added, ",") != "c" || strings.Join(replaced, ",") != "a" {
		t.Fatalf("added %v replaced %v", added, replaced)
	}
	keys, topics, raw := readGoals(t)
	if string(keys["start_prompt"]) != `"on"` || string(keys["cooldown_minutes"]) != "10" {
		t.Fatalf("other keys lost: %s", raw)
	}
	var ids []string
	for _, tp := range topics {
		ids = append(ids, tp.ID)
	}
	if strings.Join(ids, ",") != "a,b,c" || topics[0].Patterns[0] != "AA" {
		t.Fatalf("order/replace wrong: %s", raw)
	}
	if !bytes.Contains(raw, []byte("ch <- v")) {
		t.Fatalf("pattern was HTML-escaped: %s", raw)
	}
	if bak, _ := os.ReadFile(UserConfigPath() + ".bak"); !bytes.Equal(bak, orig) {
		t.Fatalf("backup: %s", bak)
	}
}

func TestAddTopicsIsAllOrNothing(t *testing.T) {
	tempHome(t)
	orig := []byte(`{"topics":[{"id":"a","patterns":["A"]}]}`)
	os.WriteFile(UserConfigPath(), orig, 0o644)
	if _, _, err := AddTopics([]*Topic{{ID: "d", Patterns: []string{"D"}}, {ID: "e"}}); err == nil {
		t.Fatal("expected error for topic without patterns")
	}
	if raw, _ := os.ReadFile(UserConfigPath()); !bytes.Equal(raw, orig) {
		t.Fatalf("file changed after failed add: %s", raw)
	}

	os.WriteFile(UserConfigPath(), []byte(`{broken`), 0o644)
	if _, _, err := AddTopics([]*Topic{{ID: "d", Patterns: []string{"D"}}}); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("malformed goals.json: %v", err)
	}
	if _, _, err := RemoveTopics([]string{"a"}); err == nil {
		t.Fatal("remove must refuse a malformed goals.json too")
	}
	if raw, _ := os.ReadFile(UserConfigPath()); string(raw) != `{broken` {
		t.Fatalf("malformed file was overwritten: %s", raw)
	}
}

func TestAddTopicsNilCreatesEmptyConfig(t *testing.T) {
	tempHome(t)
	if _, _, err := AddTopics(nil); err != nil {
		t.Fatal(err)
	}
	_, topics, raw := readGoals(t)
	if len(topics) != 0 || !bytes.Contains(raw, []byte(`"topics": []`)) {
		t.Fatalf("want empty topic list, got %s", raw)
	}
	if _, err := os.Stat(UserConfigPath() + ".bak"); err == nil {
		t.Fatal("no backup expected when there was no file")
	}
}

func TestRemoveTopicsKeepsTheRest(t *testing.T) {
	tempHome(t)
	os.WriteFile(UserConfigPath(), []byte(`{"start_prompt":"off","topics":[{"id":"a","patterns":["A"]},{"id":"b","patterns":["B"]}]}`), 0o644)
	removed, missing, err := RemoveTopics([]string{"B", "zz"})
	if err != nil || strings.Join(removed, ",") != "b" || strings.Join(missing, ",") != "zz" {
		t.Fatalf("removed %v missing %v err %v", removed, missing, err)
	}
	keys, topics, raw := readGoals(t)
	if len(topics) != 1 || topics[0].ID != "a" || string(keys["start_prompt"]) != `"off"` {
		t.Fatalf("after remove: %s", raw)
	}
	if _, _, err := RemoveTopics([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, topics, raw = readGoals(t); len(topics) != 0 || !bytes.Contains(raw, []byte(`"topics": []`)) {
		t.Fatalf("removing the last topic must leave an empty list: %s", raw)
	}
}

func TestRemoveTopicsWithoutConfigWritesNothing(t *testing.T) {
	tempHome(t)
	if _, missing, err := RemoveTopics([]string{"a"}); err != nil || len(missing) != 1 {
		t.Fatalf("missing %v err %v", missing, err)
	}
	if _, err := os.Stat(UserConfigPath()); err == nil {
		t.Fatal("remove must not create goals.json")
	}
}

func TestListTopicsMarksSelectedAndCustom(t *testing.T) {
	tempHome(t)
	catalog := []*Topic{{ID: "sql", Name: "SQL", Mode: "quiz-after", Patterns: []string{"x"}}, {ID: "testing", Patterns: []string{"y"}}}
	os.WriteFile(UserConfigPath(), []byte(`{"topics":[{"id":"sql","patterns":["x"]},{"id":"react-hooks","name":"React hooks","patterns":["useState"]}]}`), 0o644)
	list, err := ListTopics(catalog)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]TopicListing{}
	for _, l := range list {
		got[l.ID] = l
	}
	if !got["sql"].Selected || got["sql"].Source != "catalog" || got["sql"].Mode != "quiz-after" {
		t.Fatalf("sql: %+v", got["sql"])
	}
	if got["testing"].Selected || got["testing"].Mode != "ask-first" || got["testing"].Name != "testing" {
		t.Fatalf("testing: %+v", got["testing"])
	}
	if !got["react-hooks"].Selected || got["react-hooks"].Source != "custom" {
		t.Fatalf("custom: %+v", got["react-hooks"])
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/ -run 'TestAddTopics|TestRemoveTopics|TestListTopics' -v`
Expected: FAIL to compile with `undefined: AddTopics`, `undefined: decodeUserConfig`, etc.

- [ ] **Step 3: Write minimal implementation**

In `internal/core/topics.go`, extend the imports to:

```go
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
```

Append:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `gofmt -w internal/core && go test ./internal/core/ -v`
Expected: PASS (all core tests, old and new).

- [ ] **Step 5: Commit**

```bash
git add internal/core/topics.go internal/core/topics_test.go
git commit -m "Add validated goals.json editing: add, remove and list topics"
```

---

### Task 3: Built-in topic catalog

**Files:**
- Create: `assets/topics.json`
- Modify: `assets/embed.go`
- Modify: `internal/core/topics.go`
- Test: `internal/core/topics_test.go`

**Interfaces:**
- Consumes: `ParseTopicsJSON`, `ValidateTopic`, `AddTopics`, `LoadConfig`, `Topic.Matches`, `indexOf`.
- Produces:
  - `assets.Topics []byte` (embedded `assets/topics.json`)
  - `var DefaultTopicIDs = []string{"concurrency", "architecture"}`
  - `func ParseCatalog(raw []byte) ([]*Topic, error)`
  - `func CatalogTopics(catalog []*Topic, ids []string) ([]*Topic, error)`: ids normalised, duplicates dropped, unknown ids error listing valid ones.

- [ ] **Step 1: Write the failing test**

Append to `internal/core/topics_test.go` (add import `"github.com/ryangovender/git-prompted/assets"`):

```go
func catalog(t *testing.T) []*Topic {
	t.Helper()
	c, err := ParseCatalog(assets.Topics)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Each catalog topic must fire on code that exercises it and stay quiet on plain code.
func TestCatalogIsValidAndTargeted(t *testing.T) {
	tempHome(t)
	c := catalog(t)
	if _, _, err := AddTopics(c); err != nil {
		t.Fatal(err)
	}
	cfg := LoadConfig(t.TempDir(), func(w string) { t.Errorf("warn: %s", w) })
	if cfg == nil || len(cfg.Topics) != len(c) {
		t.Fatal("catalog did not round-trip through goals.json")
	}
	hits := map[string]string{
		"concurrency":    "go func() { ch <- 1 }()",
		"architecture":   "type Store interface { Get() }",
		"error-handling": `return fmt.Errorf("load: %w", err)`,
		"sql":            `rows, err := db.QueryContext(ctx, "SELECT id FROM users")`,
		"testing":        "func TestParse(t *testing.T) {",
		"security":       "hash, _ := bcrypt.GenerateFromPassword(pw, 12)",
		"performance":    "func BenchmarkParse(b *testing.B) {",
		"api-design":     `mux.HandleFunc("/users", listUsers)`,
	}
	plain := []string{
		"x := 1",
		"if err != nil {\n\treturn err\n}",
		"return nil",
		"func add(a, b int) int { return a + b }",
		`fmt.Println("hello")`,
	}
	for _, tp := range cfg.Topics {
		code, ok := hits[tp.ID]
		if !ok {
			t.Errorf("no fixture for catalog topic %s; add one", tp.ID)
			continue
		}
		if !tp.Matches([]string{"x.go"}, code) {
			t.Errorf("%s should match %q", tp.ID, code)
		}
		for _, p := range plain {
			if tp.Matches([]string{"x.go"}, p) {
				t.Errorf("%s fires on plain code %q", tp.ID, p)
			}
		}
	}
	for _, id := range DefaultTopicIDs {
		if indexOf(c, id) < 0 {
			t.Errorf("default topic %s missing from catalog", id)
		}
	}
}

func TestCatalogTopicsPicksByID(t *testing.T) {
	c := catalog(t)
	got, err := CatalogTopics(c, []string{"Error Handling", "sql", "sql"})
	if err != nil || len(got) != 2 || got[0].ID != "error-handling" || got[1].ID != "sql" {
		t.Fatalf("got %v err %v", got, err)
	}
	if _, err := CatalogTopics(c, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "concurrency") {
		t.Fatalf("unknown id should list valid ids: %v", err)
	}
	if _, err := ParseCatalog([]byte(`[{"id":"a","patterns":["x"]},{"id":"a","patterns":["y"]}]`)); err == nil {
		t.Fatal("duplicate catalog ids must fail")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core/ -run 'TestCatalog' -v`
Expected: FAIL to compile with `undefined: assets.Topics`, `undefined: ParseCatalog`.

- [ ] **Step 3: Write the catalog and implementation**

Create `assets/topics.json`:

```json
[
  {
    "id": "concurrency",
    "name": "Concurrency",
    "mode": "ask-first",
    "level": "intermediate",
    "goals": [
      "Reason about goroutine/task lifetimes and how they end",
      "Choose between channels, mutexes and errgroup with a reason",
      "Spot races, leaks and missing cancellation before they ship"
    ],
    "patterns": [
      "\\bgo func\\b",
      "\\bchan\\b",
      "sync\\.(Mutex|RWMutex|WaitGroup|Once|Cond)",
      "errgroup\\.",
      "\\bselect\\s*\\{",
      "asyncio\\.",
      "threading\\.",
      "Promise\\.(all|race|any)"
    ],
    "concepts": ["goroutine-lifetime", "cancellation", "channel-ownership", "data-race", "goroutine-leak", "backpressure", "error-propagation", "mutex-vs-channel"]
  },
  {
    "id": "architecture",
    "name": "Architecture & boundaries",
    "mode": "quiz-after",
    "level": "advanced",
    "goals": [
      "Explain why a boundary or interface exists and who owns it",
      "Keep domain logic independent of transport and storage",
      "Weigh the trade-offs of a design, not just its happy path"
    ],
    "patterns": [
      "type \\w+ interface\\s*\\{",
      "class \\w+\\(ABC\\)",
      "export interface \\w+"
    ],
    "concepts": ["dependency-direction", "boundary-ownership", "interface-placement", "domain-isolation", "testing-seams", "coupling", "trade-offs"]
  },
  {
    "id": "error-handling",
    "name": "Error handling",
    "mode": "ask-first",
    "level": "intermediate",
    "goals": [
      "Decide whether to handle, wrap or return an error, and say why",
      "Add context when wrapping without losing the original error",
      "Design failure paths callers can act on (sentinels, types, errors.Is/As)"
    ],
    "patterns": [
      "errors\\.(Is|As|New|Join|Unwrap)\\(",
      "fmt\\.Errorf\\(.*%w",
      "\\bpanic\\(",
      "\\brecover\\(\\)",
      "\\bexcept\\s+\\w+",
      "\\braise\\s+\\w+",
      "\\bcatch\\s*\\("
    ],
    "concepts": ["error-wrapping", "sentinel-errors", "error-types", "errors-is-as", "panic-vs-error", "error-context", "fail-fast", "partial-failure"]
  },
  {
    "id": "sql",
    "name": "SQL & data access",
    "mode": "quiz-after",
    "level": "intermediate",
    "goals": [
      "Write queries that are correct under concurrent writes (transactions, isolation)",
      "Know which index a query needs and why",
      "Write migrations that are safe to run on a live database"
    ],
    "patterns": [
      "(?i)\\bselect\\b[\\s\\S]+?\\bfrom\\b",
      "(?i)\\binsert\\s+into\\b",
      "(?i)\\bupdate\\s+\\w+\\s+set\\b",
      "(?i)\\bdelete\\s+from\\b",
      "(?i)\\b(create|alter|drop)\\s+(table|index)\\b",
      "\\.(Query|QueryRow|Exec)(Context)?\\(",
      "\\.BeginTx?\\(",
      "database/sql"
    ],
    "concepts": ["transactions", "isolation-levels", "indexing", "n-plus-one", "sql-injection", "safe-migrations", "locking", "connection-pooling"]
  },
  {
    "id": "testing",
    "name": "Testing",
    "mode": "quiz-after",
    "level": "intermediate",
    "goals": [
      "Decide what a test must prove before writing it",
      "Pick the right seam: unit, integration or end-to-end",
      "Write tests that fail for the right reason and do not flake"
    ],
    "patterns": [
      "\\bfunc (Test|Fuzz)\\w*\\(\\w+ \\*testing\\.[TF]\\)",
      "\\bt\\.Run\\(",
      "\\bdef test_\\w+",
      "@pytest\\.(fixture|mark)",
      "\\b(describe|it|test)\\(\\s*['\"\\x60]",
      "\\bexpect\\(.+\\)\\.(to|not)",
      "httptest\\."
    ],
    "concepts": ["test-boundaries", "table-driven-tests", "test-doubles", "flaky-tests", "assertion-quality", "test-isolation", "coverage-vs-confidence"]
  },
  {
    "id": "security",
    "name": "Security",
    "mode": "ask-first",
    "level": "intermediate",
    "goals": [
      "Treat every external input as hostile until validated",
      "Store and compare secrets and passwords safely",
      "Know the difference between authentication and authorisation in this code"
    ],
    "patterns": [
      "golang\\.org/x/crypto|\\bcrypto/(rand|subtle|hmac|sha256|aes)",
      "\\b(bcrypt|argon2|scrypt)\\.",
      "(?i)\\b(jwt|jsonwebtoken)\\b",
      "(?i)\\b(password|passwd|secret|api_?key)\\w*\\s*:?=",
      "exec\\.Command(Context)?\\(|\\bsubprocess\\.|child_process|\\bos\\.system\\(",
      "\\.innerHTML\\s*=|dangerouslySetInnerHTML|template\\.HTML\\(",
      "http\\.SetCookie\\(|(?i)set-cookie"
    ],
    "concepts": ["input-validation", "injection", "authn-vs-authz", "secret-handling", "password-hashing", "least-privilege", "output-encoding", "session-security"]
  },
  {
    "id": "performance",
    "name": "Performance",
    "mode": "quiz-after",
    "level": "intermediate",
    "goals": [
      "Measure before optimising, and know what the measurement says",
      "Spot allocations and complexity that matter on the hot path",
      "Know when a cache helps and what it costs"
    ],
    "patterns": [
      "func Benchmark\\w*\\(\\w+ \\*testing\\.B\\)",
      "\\bsync\\.Pool\\b",
      "\\bpprof\\b",
      "\\bmake\\(\\[\\][^,()]+,\\s*0\\s*,",
      "(?i)\\b(lru_?)?cache\\b",
      "\\buse(Memo|Callback)\\(|\\bReact\\.memo\\("
    ],
    "concepts": ["measure-first", "allocation", "algorithmic-complexity", "caching", "batching", "profiling", "memoization", "hot-path"]
  },
  {
    "id": "api-design",
    "name": "API design",
    "mode": "ask-first",
    "level": "intermediate",
    "goals": [
      "Shape requests and responses around what callers need, not the storage model",
      "Pick status codes and error shapes callers can act on",
      "Change an API without breaking existing clients"
    ],
    "patterns": [
      "\\.HandleFunc\\(|\\bhttp\\.Handle\\(",
      "\\b(app|router|r|mux)\\.(Get|Post|Put|Patch|Delete|get|post|put|patch|delete)\\(\\s*['\"\\x60]",
      "@(app|router)\\.(get|post|put|patch|delete|route)\\(",
      "\\bw\\.WriteHeader\\(|\\bhttp\\.Error\\(",
      "^\\s*rpc \\w+\\s*\\(",
      "(?i)^openapi:\\s"
    ],
    "concepts": ["resource-modelling", "status-codes", "versioning", "idempotency", "pagination", "error-contract", "backward-compatibility"]
  }
]
```

In `assets/embed.go`, add below the existing `GoalsExample` embed (it is removed in Task 5):

```go
//go:embed topics.json
var Topics []byte
```

Append to `internal/core/topics.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `gofmt -w internal/core assets && go test ./internal/core/ -v`
Expected: PASS. If a fixture fails, fix the pattern in `assets/topics.json`, not the fixture; if a plain-code line fires, narrow the pattern.

- [ ] **Step 5: Commit**

```bash
git add assets/topics.json assets/embed.go internal/core/topics.go internal/core/topics_test.go
git commit -m "Add built-in topic catalog with targeted patterns per topic"
```

---

### Task 4: Topics without paths ignore prose-only edits

**Files:**
- Modify: `internal/core/config.go` (`Topic.Matches`, imports)
- Create: `internal/core/config_test.go`

**Interfaces:**
- Consumes: `LoadConfig`, `UserConfigPath`, `tempHome` (test helper from Task 2).
- Produces: changed behaviour of `func (t *Topic) Matches(files []string, code string) bool`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/config_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestProseEdits -v`
Expected: FAIL with "prose-only edit matched a topic without paths".

- [ ] **Step 3: Write minimal implementation**

In `internal/core/config.go`, add `"path"` to the imports, and add above `Matches`:

```go
// proseExt marks files that are writing, not code. Docs quote code all the time,
// so a topic only fires on them when its paths ask for them.
var proseExt = map[string]bool{".md": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true}

func allProse(files []string) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		if !proseExt[strings.ToLower(path.Ext(f))] {
			return false
		}
	}
	return true
}
```

Then make the first statement of `Matches`:

```go
	if len(t.paths) == 0 && allProse(files) {
		return false
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `gofmt -w internal/core && go test ./...`
Expected: PASS (all packages).

- [ ] **Step 5: Commit**

```bash
git add internal/core/config.go internal/core/config_test.go
git commit -m "Skip prose-only edits for topics without paths"
```

---

### Task 5: `topics` command and new `init`

**Files:**
- Create: `cmd/git-prompted/topics.go`
- Modify: `cmd/git-prompted/main.go` (usage, `splitArgs`, flags, `init`, new `topics` case, `status` message)
- Modify: `cmd/git-prompted/main_test.go` (`setup` helper, new tests)
- Modify: `assets/embed.go` (remove `GoalsExample`)
- Delete: `assets/goals.example.json`

**Interfaces:**
- Consumes: `core.ParseCatalog`, `core.CatalogTopics`, `core.AddTopics`, `core.RemoveTopics`, `core.ListTopics`, `core.ParseTopicsJSON`, `core.TopicListing`, `core.DefaultTopicIDs`, `assets.Topics`.
- Produces:
  - `var stdin io.Reader = os.Stdin` (package `main`, replaced in tests)
  - `func runTopics(w io.Writer, in io.Reader, args []string, asJSON bool) error`
  - test helper `setupEmpty(t *testing.T) string` (home with no `goals.json`)
  - CLI: `init [--defaults|--empty]`, `topics list [--json] | add <id>... | add --json - | remove <id>...`

- [ ] **Step 1: Write the failing tests**

In `cmd/git-prompted/main_test.go`, replace the `setup` function and drop the now-unused `assets` import:

```go
// setupEmpty is a fresh home with no goals.json (a brand-new user).
func setupEmpty(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GIT_PROMPTED_HOME", home)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("GEMINI_PROJECT_DIR", "")
	return home
}

// setup is a home with the default topics (concurrency + architecture).
func setup(t *testing.T) string {
	t.Helper()
	home := setupEmpty(t)
	cmd(t, "init", "--defaults")
	return home
}
```

Append (and add `"fmt"` to the test imports):

```go
func TestInitVariants(t *testing.T) {
	home := setupEmpty(t)
	goals := filepath.Join(home, "goals.json")
	cmd(t, "init")
	if _, err := os.Stat(goals); err == nil {
		t.Fatal("plain init must not write topics the user did not pick")
	}
	var out bytes.Buffer
	if err := status(&out, "/p", ""); err != nil || !strings.Contains(out.String(), "topics add") {
		t.Fatalf("status should explain how to pick topics: %v\n%s", err, out.String())
	}
	if err := runCommand([]string{"init", "--defaults", "--empty"}); err == nil {
		t.Fatal("--defaults with --empty must be rejected")
	}
	cmd(t, "init", "--empty")
	raw, _ := os.ReadFile(goals)
	if !strings.Contains(string(raw), `"topics": []`) {
		t.Fatalf("init --empty: %s", raw)
	}
	cmd(t, "init", "--defaults") // config exists: must not overwrite
	if again, _ := os.ReadFile(goals); string(again) != string(raw) {
		t.Fatalf("init overwrote an existing config: %s", again)
	}
}

func TestTopicsCommand(t *testing.T) {
	home := setupEmpty(t)
	var out bytes.Buffer
	if err := runTopics(&out, nil, []string{"add", "sql", "error-handling"}, false); err != nil {
		t.Fatal(err)
	}
	custom := `{"id":"react-hooks","name":"React hooks","patterns":["\\buse(State|Effect)\\("]}`
	if err := runTopics(&out, strings.NewReader(custom), []string{"add", "-"}, true); err != nil {
		t.Fatal(err)
	}
	if err := runTopics(&out, strings.NewReader(`{oops`), []string{"add", "-"}, true); err == nil || !strings.Contains(err.Error(), "invalid topic JSON") {
		t.Fatalf("bad JSON: %v", err)
	}
	if err := runTopics(&out, nil, []string{"add", "nope"}, false); err == nil || !strings.Contains(err.Error(), "choose from") {
		t.Fatalf("unknown id: %v", err)
	}
	out.Reset()
	if err := runTopics(&out, nil, []string{"list"}, true); err != nil {
		t.Fatal(err)
	}
	var list []core.TopicListing
	if err := json.Unmarshal(out.Bytes(), &list); err != nil {
		t.Fatalf("list --json: %v\n%s", err, out.String())
	}
	sel := map[string]string{}
	for _, l := range list {
		if l.Selected {
			sel[l.ID] = l.Source
		}
	}
	if sel["sql"] != "catalog" || sel["error-handling"] != "catalog" || sel["react-hooks"] != "custom" || len(sel) != 3 {
		t.Fatalf("selected: %v", sel)
	}

	// The form prompts print: --session first, `-` as the stdin marker, no known session needed.
	stdin = strings.NewReader(`{"id":"go-generics","patterns":["\\[\\w+ (any|comparable)\\]"]}`)
	defer func() { stdin = os.Stdin }()
	cmd(t, "--session", "unknown-session", "topics", "add", "--json", "-")

	cmd(t, "topics", "remove", "sql")
	raw, _ := os.ReadFile(filepath.Join(home, "goals.json"))
	if strings.Contains(string(raw), `"id": "sql"`) || !strings.Contains(string(raw), `"id": "go-generics"`) {
		t.Fatalf("goals.json after add/remove:\n%s", raw)
	}

	edit := `{"session_id":"t1","cwd":"/p","tool_name":"Write","tool_input":{"file_path":"/p/%s","content":"return fmt.Errorf(\"load: %%w\", err)"}}`
	if out := hook(t, "claude", "before-edit", fmt.Sprintf(edit, "w.go")); !strings.Contains(out, "Learning checkpoint: Error handling") {
		t.Fatalf("picked topic should fire on code: %s", out)
	}
	cmd(t, "decide", "--session", "t1", "--topic", "error-handling", "--choice", "skip")
	cmd(t, "topics", "add", "concurrency")
	prose := `{"session_id":"t2","cwd":"/p","tool_name":"Write","tool_input":{"file_path":"/p/spec.md","content":"go func() { ch <- 1 }()"}}`
	if out := hook(t, "claude", "before-edit", prose); out != "" {
		t.Fatalf("prose edit fired: %s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/git-prompted/ -v`
Expected: FAIL to compile with `undefined: runTopics` and `undefined: stdin`.

- [ ] **Step 3: Write the implementation**

Create `cmd/git-prompted/topics.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ryangovender/git-prompted/assets"
	"github.com/ryangovender/git-prompted/internal/core"
)

// stdin is where `topics add --json -` reads from; tests replace it.
var stdin io.Reader = os.Stdin

const topicsUsage = "usage: git-prompted topics list [--json] | add <id>... | add --json - | remove <id>..."

// runTopics lists the catalog and adds or removes topics in goals.json.
// Agents run it during onboarding; every write is validated by core.
func runTopics(w io.Writer, in io.Reader, args []string, asJSON bool) error {
	catalog, err := core.ParseCatalog(assets.Topics)
	if err != nil {
		return fmt.Errorf("built-in catalog: %v", err)
	}
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list":
		list, err := core.ListTopics(catalog)
		if err != nil {
			return err
		}
		if asJSON {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		for _, l := range list {
			mark := " "
			if l.Selected {
				mark = "*"
			}
			fmt.Fprintf(w, "%s %-16s %-11s %s\n", mark, l.ID, l.Mode, l.Name)
		}
		fmt.Fprintln(w, "\n* = in your goals.json. Add: git-prompted topics add <id>...  Remove: git-prompted topics remove <id>...")
		return nil
	case "add":
		var topics []*core.Topic
		if asJSON {
			if len(args) > 1 || (len(args) == 1 && args[0] != "-") {
				return fmt.Errorf("pass catalog ids or --json - (topic JSON on stdin), not both")
			}
			raw, err := io.ReadAll(in)
			if err != nil {
				return err
			}
			if topics, err = core.ParseTopicsJSON(raw); err != nil {
				return err
			}
		} else {
			if len(args) == 0 {
				return fmt.Errorf(topicsUsage)
			}
			if topics, err = core.CatalogTopics(catalog, args); err != nil {
				return err
			}
		}
		added, replaced, err := core.AddTopics(topics)
		if err != nil {
			return err
		}
		report(w, "Added", added)
		report(w, "Replaced", replaced)
		return nil
	case "remove":
		if len(args) == 0 {
			return fmt.Errorf(topicsUsage)
		}
		removed, missing, err := core.RemoveTopics(args)
		if err != nil {
			return err
		}
		report(w, "Removed", removed)
		report(w, "Not in goals.json", missing)
		return nil
	}
	return fmt.Errorf(topicsUsage)
}

func report(w io.Writer, label string, ids []string) {
	if len(ids) > 0 {
		fmt.Fprintf(w, "%s: %s\n", label, strings.Join(ids, ", "))
	}
}
```

In `cmd/git-prompted/main.go`:

1. In `usage`, replace the `init` line with:

```
  git-prompted init [--defaults|--empty]    how to pick topics (--defaults: concurrency + architecture; --empty: none)
  git-prompted topics list [--json]         built-in topic catalog; * = in your goals.json
  git-prompted topics add <id>...           add catalog topics (add --json - reads custom topic JSON from stdin)
  git-prompted topics remove <id>...
```

2. In `splitArgs`, treat a lone `-` (the stdin marker) as positional. Change the first condition to:

```go
		if strings.HasPrefix(a, "-") && a != "-" {
```

3. In `runCommand`, add after the `with` flag:

```go
	defaults := fs.Bool("defaults", false, "init: start with the default topics")
	empty := fs.Bool("empty", false, "init: start with no topics")
```

4. Replace the whole `case "init":` block with:

```go
	case "init":
		path := core.UserConfigPath()
		if _, err := os.Stat(path); err == nil {
			fmt.Println("Config already exists:", path)
			return nil
		}
		catalog, err := core.ParseCatalog(assets.Topics)
		if err != nil {
			return err
		}
		switch {
		case *defaults && *empty:
			return fmt.Errorf("use --defaults or --empty, not both")
		case *defaults:
			picked, err := core.CatalogTopics(catalog, core.DefaultTopicIDs)
			if err != nil {
				return err
			}
			if _, _, err := core.AddTopics(picked); err != nil {
				return err
			}
			fmt.Printf("Created %s with %s.\n", path, strings.Join(core.DefaultTopicIDs, ", "))
		case *empty:
			if _, _, err := core.AddTopics(nil); err != nil {
				return err
			}
			fmt.Printf("Created %s with no topics. Pick some any time: /git-prompted:learn init, or git-prompted topics add <id>...\n", path)
		default:
			fmt.Println("Pick the topics you want to practise. In your agent, run /git-prompted:learn init; or from here:")
			fmt.Println()
			for _, t := range catalog {
				fmt.Printf("  %-16s %s\n", t.ID, t.DisplayName())
			}
			fmt.Println("\n  git-prompted topics add <id>...    e.g. git-prompted topics add concurrency error-handling")
		}
		return nil
	case "topics":
		return runTopics(os.Stdout, stdin, pos, *asJSON)
```

5. In `status`, replace the "No topics configured" line with:

```go
		fmt.Fprintln(w, "No topics configured. Pick some: /git-prompted:learn init in your agent, or git-prompted topics add <id>... (see git-prompted topics list)")
```

In `assets/embed.go`, delete the two lines:

```go
//go:embed goals.example.json
var GoalsExample []byte
```

Then: `git rm assets/goals.example.json`

- [ ] **Step 4: Run tests to verify they pass**

Run: `gofmt -w . && make test`
Expected: PASS. `TestFullFlowPerAgent`, `TestStatusShowsTopicModes` and the rest still pass through the new `setup` (`init --defaults` writes the same two topics).

- [ ] **Step 5: Commit**

```bash
git add -A cmd/git-prompted assets
git commit -m "Add topics command; init no longer writes topics the user did not pick"
```

---

### Task 6: Onboarding conversation in the coaching guide

**Files:**
- Modify: `assets/coaching.md` (new section after "Respect the choice first")
- Modify: `scripts/sync.sh` (skill description)
- Modify: `integrations/claude/commands/learn.md`
- Regenerated by `make sync`: `integrations/claude/skills/coaching/SKILL.md`, `GEMINI.md`

**Interfaces:**
- Produces: a `## Onboarding (picking topics)` heading in the coaching guide (Task 7's Codex test checks for `## Onboarding`).

- [ ] **Step 1: Add the section to `assets/coaching.md`**

Insert after the "Respect the choice first" section:

````markdown
## Onboarding (picking topics)

Run this when a `[git-prompted]` message says no topics are set up, or when the user runs `/git-prompted:learn init`. Use the exact command path from the message for every command below.

1. Run `git-prompted topics list --json`. It returns the built-in catalog (`selected: true` means already in their goals) plus their custom topics.
2. Ask once, multi-select: "Which topics do you want to practise instead of having them written for you?" Options: the catalog names (pre-marked on a re-run), "Something else", and "Not now". If your question tool limits the number of options, name the catalog topics in the question and let them type ids.
3. Save new picks with `git-prompted topics add <id>...`. On a re-run, also run `git-prompted topics remove <id>...` for topics they unticked. Never remove a topic they did not untick: their history stays either way.
4. **Something else:** draft one topic and show it in three or four lines (name, goals, what code will trigger it). Patterns are RE2 regexes matched against the code being written; aim them at code that shows the skill (`useEffect\(` for React effects), not code that appears in almost every edit. On a yes, save it:
   ```sh
   printf '%s' '{"id":"react-hooks","name":"React hooks","mode":"ask-first","level":"intermediate","goals":["Know when an effect re-runs and why"],"patterns":["\\buse(State|Effect|Memo|Callback)\\("],"concepts":["effect-dependencies","stale-closure"]}' | git-prompted topics add --json -
   ```
   If the command rejects it, fix the field the error names and retry; do not ask the user to debug regexes.
5. **Not now:** on a first run, run `git-prompted init --empty` so they are not asked again, and mention that `/git-prompted:learn init` brings this back.
6. Confirm in one line ("Practising: Error handling, SQL. Change any time with /git-prompted:learn init.") and carry on with their request. No lecture about how checkpoints work.

Modes: `ask-first` pauses before the edit with one question; `quiz-after` lets the edit through and offers a short quiz at the end of the turn. Keep the catalog's modes unless the user asks.
````

- [ ] **Step 2: Update the skill description in `scripts/sync.sh`**

Replace the `description:` printf line with:

```sh
  printf '%s\n' 'description: How to run git prompted onboarding, learning checkpoints and quizzes, including grading with concept tags. Use this whenever a [git-prompted] hook message appears (a blocked edit marked "Learning checkpoint", a stop message offering a quiz, or a request to run onboarding and pick topics), whenever the user runs /git-prompted:quiz or /git-prompted:learn init, or whenever the user says they want to think something through themselves, be tested, or practise a topic instead of having the code written for them.'
```

- [ ] **Step 3: Update `integrations/claude/commands/learn.md`**

Replace the line

```
- `status`, `progress`, `init` -> same name. For `progress`, also show the user the contents of the file it prints.
```

with

```
- `init` -> run topic onboarding: follow the "Onboarding" section of the git-prompted coaching skill, using the CLI above for every command.
- `status`, `progress` -> same name. For `progress`, also show the user the contents of the file it prints.
```

- [ ] **Step 4: Regenerate and verify**

Run: `make sync && make check && make test`
Expected: `synced v0.3.3`, then `make check` exits 0 (generated copies match), then tests PASS. `grep -c "## Onboarding" GEMINI.md integrations/claude/skills/coaching/SKILL.md` prints `1` for each.

- [ ] **Step 5: Commit**

```bash
git add assets/coaching.md scripts/sync.sh integrations/claude GEMINI.md
git commit -m "Describe the onboarding conversation in the coaching guide"
```

---

### Task 7: Onboarding at session start

**Files:**
- Modify: `internal/core/topics.go` (`NeedsOnboarding`)
- Modify: `internal/core/engine.go` (`OnboardingStart`)
- Modify: `cmd/git-prompted/main.go` (`runHook`)
- Test: `internal/core/topics_test.go`, `cmd/git-prompted/main_test.go`

**Interfaces:**
- Consumes: `UserConfigPath`, `Engine.SessionStart`, `Engine.cli`, `askHint`, `assets.Coaching`; `## Onboarding` heading from Task 6.
- Produces:
  - `func NeedsOnboarding(source string) bool`
  - `func (e *Engine) OnboardingStart(ev Event) (string, error)`: `e.Cfg` may be nil.

- [ ] **Step 1: Write the failing tests**

Append to `internal/core/topics_test.go`:

```go
func TestNeedsOnboarding(t *testing.T) {
	tempHome(t)
	for src, want := range map[string]bool{"": true, "startup": true, "clear": true, "resume": false, "compact": false} {
		if got := NeedsOnboarding(src); got != want {
			t.Errorf("source %q: got %v", src, got)
		}
	}
	os.WriteFile(UserConfigPath(), []byte(`{"topics":[]}`), 0o644)
	if NeedsOnboarding("startup") {
		t.Fatal("an existing goals.json (even empty) means onboarding is done or declined")
	}
}
```

Append to `cmd/git-prompted/main_test.go`:

```go
func TestOnboardingWhenNoConfig(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "gemini"} {
		t.Run(agent, func(t *testing.T) {
			setupEmpty(t)
			start := hook(t, agent, "session-start", `{"session_id":"o1","source":"startup"}`)
			for _, want := range []string{"no learning topics are set up yet", "topics list --json", "topics add --json -", "init --empty"} {
				if !strings.Contains(start, want) {
					t.Fatalf("missing %q in: %s", want, start)
				}
			}
			if agent == "codex" && !strings.Contains(start, "## Onboarding") {
				t.Fatal("codex should get the coaching guide inlined")
			}
			if out := hook(t, agent, "session-start", `{"session_id":"o1","source":"resume"}`); out != "" && out != "{}" {
				t.Fatalf("resume must not onboard: %s", out)
			}
		})
	}
}

func TestDecliningOrRemovingAllTopicsStaysQuiet(t *testing.T) {
	setupEmpty(t)
	cmd(t, "init", "--empty")
	if out := hook(t, "claude", "session-start", `{"session_id":"o2","source":"startup"}`); out != "" {
		t.Fatalf("declined onboarding must stay quiet: %s", out)
	}
	cmd(t, "topics", "add", "sql")
	cmd(t, "topics", "remove", "sql")
	if out := hook(t, "claude", "session-start", `{"session_id":"o3","source":"startup"}`); out != "" {
		t.Fatalf("no topics left must stay quiet: %s", out)
	}
}

func TestOnboardingKeepsProjectTopics(t *testing.T) {
	setupEmpty(t)
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, ".git-prompted.json"), []byte(`{"topics":[{"id":"hexagonal","name":"Ports and adapters","paths":["internal/adapters/**"]}]}`), 0o644)
	start := hook(t, "claude", "session-start", `{"session_id":"o4","cwd":"`+proj+`","source":"startup"}`)
	if !strings.Contains(start, "Ports and adapters") || !strings.Contains(start, "no learning topics are set up yet") {
		t.Fatalf("want project topics and onboarding: %s", start)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./...`
Expected: FAIL to compile with `undefined: NeedsOnboarding` (core). Once that exists, the main tests fail with empty session-start output.

- [ ] **Step 3: Write the implementation**

Append to `internal/core/topics.go`:

```go
// NeedsOnboarding reports whether a session start should run topic onboarding: a
// fresh session and no personal goals.json yet. Declining writes an empty goals.json,
// so the user is asked once.
func NeedsOnboarding(source string) bool {
	if source != "" && source != "startup" && source != "clear" {
		return false
	}
	_, err := os.Stat(UserConfigPath())
	return errors.Is(err, os.ErrNotExist)
}
```

Append to `internal/core/engine.go`, after `SessionStart`:

```go
// OnboardingStart is SessionStart for a user without a goals.json: it asks the agent
// to run topic onboarding. Cfg may be nil, or hold project-only topics whose usual
// session text is kept.
func (e *Engine) OnboardingStart(ev Event) (string, error) {
	cli := e.cli(ev.SessionID)
	text := fmt.Sprintf("[git-prompted] Installed, but no learning topics are set up yet. Before handling the user's first request, run onboarding: "+
		"ask which topics they want to practise %s, following the \"Onboarding\" section of the git-prompted coaching guide. "+
		"Catalog: `%s topics list --json`. Save picks: `%s topics add <id>...`; custom topics: `%s topics add --json -` (topic JSON on stdin). "+
		"If they say not now: `%s init --empty`. Then continue with their request.", askHint(ev.Agent), cli, cli, cli, cli)
	if e.Cfg != nil {
		base, err := e.SessionStart(ev)
		if err != nil {
			return "", err
		}
		if base != "" {
			return base + "\n" + text, nil // base already carries the guide for Codex
		}
	}
	if ev.Agent == "codex" && e.Coaching != "" {
		text += "\n\nCoaching guide:\n\n" + e.Coaching
	}
	return text, nil
}
```

In `cmd/git-prompted/main.go` `runHook`, replace

```go
	cfg := core.LoadConfig(ev.ProjectDir, warn)
	if cfg == nil {
		return 0
	}
```

with

```go
	cfg := core.LoadConfig(ev.ProjectDir, warn)
	if h == adapter.SessionStart && core.NeedsOnboarding(ev.Source) {
		// No goals.json yet: instead of staying inert, ask the agent to let the user pick topics.
		eng := &core.Engine{Cfg: cfg, Exe: stablePath(), Coaching: assets.Coaching}
		text, err := eng.OnboardingStart(ev)
		if err != nil {
			warn("hook error ignored: " + err.Error())
			return 0
		}
		resp = ad.Context(text)
		return 0
	}
	if cfg == nil {
		return 0
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `gofmt -w . && make test`
Expected: PASS.

Manual check (temp home, real binary):

```sh
make build
export GIT_PROMPTED_HOME=$(mktemp -d)
printf '%s' '{"session_id":"t","cwd":"/p","source":"startup"}' | ./dist/git-prompted hook claude session-start
./dist/git-prompted topics add error-handling sql && ./dist/git-prompted topics list
printf '%s' '{"session_id":"t","cwd":"/p","source":"startup"}' | ./dist/git-prompted hook claude session-start
```

Expected: the first call prints onboarding JSON; `topics list` marks `error-handling` and `sql` with `*`; the third call prints the normal "Learning checkpoints on for this session?" context.

- [ ] **Step 5: Commit**

```bash
git add internal/core/topics.go internal/core/topics_test.go internal/core/engine.go cmd/git-prompted/main.go cmd/git-prompted/main_test.go
git commit -m "Run topic onboarding at session start when no goals.json exists"
```

---

### Task 8: Docs, version 0.4.0 and pull request

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `VERSION`
- Regenerated by `make sync`: `bin/git-prompted`, `integrations/claude/bin/git-prompted`, `gemini-extension.json`, `integrations/claude/.claude-plugin/plugin.json`

- [ ] **Step 1: README**

Replace

````markdown
Then set your goals:
```sh
git prompted init      # writes ~/.git-prompted/goals.json, then edit it
```
````

with

````markdown
Then pick your topics. The first time you start a session, your agent asks which topics you want to practise (from a built-in catalog, or describe your own and it drafts the topic for you) and saves them. Change them any time with `/git-prompted:learn init` in Claude Code, or ask your agent to "run git prompted onboarding". From a terminal:
```sh
git prompted topics list                     # built-in catalog; * = already in your goals
git prompted topics add sql error-handling
git prompted topics remove architecture
git prompted init --defaults                 # skip picking: concurrency + architecture
```
````

Replace the command-list line

```
git prompted init | status | stats [--json] | progress | coaching | version
```

with

```
git prompted init [--defaults|--empty] | topics list|add|remove | status | stats [--json] | progress | coaching | version
```

- [ ] **Step 2: CLAUDE.md**

In the Layout block, replace

```
  config.go            goals.json + .git-prompted.json merge, topic matching, ** globs
```

with

```
  config.go            goals.json + .git-prompted.json merge, topic matching (prose files skipped), ** globs
  topics.go            topic catalog, validation, goals.json edits (topics add/remove), onboarding trigger
```

and replace

```
assets/                embedded into the binary: goals.example.json, coaching.md
```

with

```
assets/                embedded into the binary: topics.json (topic catalog), coaching.md
```

In Conventions, add:

```
- Topics reach `goals.json` only through `core.AddTopics` / `RemoveTopics` (validated, locked, atomic, backed up). Don't write `goals.json` directly. New catalog topics need a must-match fixture in `TestCatalogIsValidAndTargeted`.
- Onboarding (picking topics) is triggered by a missing `goals.json`; declining writes `{"topics": []}`. The conversation lives in `assets/coaching.md`.
```

- [ ] **Step 3: Version**

```sh
printf '0.4.0\n' > VERSION
make sync
```

Expected: `synced v0.4.0`.

- [ ] **Step 4: Full verification**

Run: `gofmt -l . ; make test && make check && make build && ./dist/git-prompted version`
Expected: `gofmt -l` prints nothing; tests PASS; `make check` exits 0; prints `git-prompted 0.4.0`.

- [ ] **Step 5: Commit, push, open PR**

```bash
git add README.md CLAUDE.md VERSION bin integrations gemini-extension.json
git commit -m "Document topic onboarding and bump version to 0.4.0"
git push -u origin feature/topic-onboarding
gh pr create --title "Let users pick their learning topics" --body "Implements docs/superpowers/specs/2026-10-13-topic-onboarding-design.md: topic catalog, validated \`topics\` command, onboarding at first session, prose files skipped. Existing users with a goals.json see no change until they run /git-prompted:learn init."
```

Tagging and releasing `v0.4.0` happens after the PR is merged and the user approves the release.
