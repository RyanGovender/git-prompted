package core

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ryangovender/git-prompted/assets"
)

func TestValidateTopicRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		`{"id":"Bad ID","patterns":["x"]}`:               `kebab-case, e.g. "bad-id"`,
		`{"id":"a","patterns":["use(State"]}`:            `pattern 1 "use(State" does not compile`,
		`{"id":"a"}`:                                     "at least one pattern or path",
		`{"id":"a","mode":"sometimes","patterns":["x"]}`: "mode",
		`{"patterns":["x"]}`:                             "id is required",
		`{"id":"a","pattern":["x"]}`:                     "invalid topic JSON",
		`{"id":"a","patterns":"x"}`:                      "invalid topic JSON",
		`[]`:                                             "no topics",
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
