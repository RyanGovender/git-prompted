package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestScanRepoCountsOnlyTheUsersOwnCode(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go":                   "go func() {}()",
		"worker/pool.go":            "var mu sync.Mutex\ngo func() {}()",
		"store/db.go":               "db.Query(\"SELECT 1\")",
		"README.md":                 "go func() {}()",           // prose: skipped
		"vendor/lib/x.go":           "go func() {}()",           // dependency: skipped
		"node_modules/a/index.js":   "go func() {}()",           // dependency: skipped
		".git/hooks/pre-commit.go":  "go func() {}()",           // hidden: skipped
		"assets/blob.bin":           "go func() {}\x00\x01\x02", // binary: skipped
		"migrations/001_init.sql":   "CREATE TABLE t (id int)",
		"internal/handlers/http.go": "func h() {}",
	})
	topics := []*Topic{
		{ID: "concurrency", Patterns: []string{`\bgo func\b`, `sync\.Mutex`}},
		{ID: "sql", Patterns: []string{`(?i)\bselect\b`}},
		{ID: "migrations", Paths: []string{"migrations/**"}},
		{ID: "testing", Patterns: []string{`func Test`}},
	}
	counts, err := ScanRepo(dir, topics)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"concurrency": 2, "sql": 1, "migrations": 1}
	for id, n := range want {
		if counts[id] != n {
			t.Errorf("%s: got %d files, want %d (all: %v)", id, counts[id], n, counts)
		}
	}
	if counts["testing"] != 0 {
		t.Errorf("testing: got %d, want 0", counts["testing"])
	}
	if _, err := ScanRepo(filepath.Join(dir, "missing"), topics); err == nil {
		t.Fatal("missing directory: want an error")
	}
}

func TestListTopicsForRepoPutsRepoMatchesFirst(t *testing.T) {
	tempHome(t)
	catalog := []*Topic{
		{ID: "testing", Patterns: []string{`func Test`}},
		{ID: "sql", Patterns: []string{`SELECT`}},
		{ID: "concurrency", Patterns: []string{`go func`}},
	}
	// The user's version of sql is the one that runs, so it is the one scanned.
	os.WriteFile(UserConfigPath(), []byte(`{"topics":[{"id":"sql","patterns":["db\\.Query"]},{"id":"react-hooks","patterns":["useEffect\\("]}]}`), 0o644)
	dir := writeTree(t, map[string]string{
		"a.go":   "go func() {}()",
		"b.go":   "go func() {}()",
		"c.go":   "db.Query(x)",
		"d.go":   "SELECT", // matches only the catalog sql pattern, which the user replaced
		"ui.tsx": "useEffect(() => {}, [])",
	})
	list, err := ListTopicsForRepo(catalog, dir)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	seen := map[string]int{}
	for _, l := range list {
		order = append(order, l.ID)
		if l.SeenInRepo == nil {
			t.Fatalf("%s: seen_in_repo missing", l.ID)
		}
		seen[l.ID] = *l.SeenInRepo
	}
	wantOrder := []string{"concurrency", "sql", "react-hooks", "testing"}
	for i := range wantOrder {
		if i >= len(order) || order[i] != wantOrder[i] {
			t.Fatalf("order: got %v, want %v", order, wantOrder)
		}
	}
	if seen["concurrency"] != 2 || seen["sql"] != 1 || seen["react-hooks"] != 1 || seen["testing"] != 0 {
		t.Fatalf("counts: %v", seen)
	}

	plain, err := ListTopics(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if plain[0].ID != "testing" || plain[0].SeenInRepo != nil {
		t.Fatalf("ListTopics must keep catalog order and omit seen_in_repo: %+v", plain[0])
	}
}
