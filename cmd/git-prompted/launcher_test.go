package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// The launcher keeps ~/.git-prompted/bin/git-prompted pointing at a binary. An older
// launcher still on PATH (e.g. a Claude session started before an upgrade) must not
// move that link back from a newer release.
func TestLauncherLinkOnlyMovesForward(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher")
	}
	src, err := os.ReadFile("../../bin/git-prompted")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^VERSION="([^"]+)"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no VERSION in launcher")
	}
	version := string(m[1])
	name := "git-prompted-" + runtime.GOOS + "-" + runtime.GOARCH

	// Copy the launcher away from the repo so a local dist/ build is not picked up.
	launcher := filepath.Join(t.TempDir(), "launcher", "bin", "git-prompted")
	os.MkdirAll(filepath.Dir(launcher), 0o755)
	if err := os.WriteFile(launcher, src, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		linkTo   string // cached release version the link points at; "" for no link
		dangling bool
		want     string // version the link should point at afterwards
	}{
		{"no link", "", false, version},
		{"older release", "0.0.1", false, version},
		{"newer release", "99.0.0", false, "99.0.0"},
		{"newer release, binary gone", "99.0.0", true, version},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			stub := func(v string) string {
				p := filepath.Join(home, "bin", v, name)
				os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte("#!/bin/sh\necho stub "+v+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				return p
			}
			stub(version)
			link := filepath.Join(home, "bin", "git-prompted")
			if c.linkTo != "" {
				old := stub(c.linkTo)
				if c.dangling {
					os.Remove(old)
				}
				if err := os.Symlink(old, link); err != nil {
					t.Fatal(err)
				}
			}
			run := exec.Command("sh", launcher, "version")
			run.Env = append(os.Environ(), "GIT_PROMPTED_HOME="+home, "GIT_PROMPTED_BIN=")
			out, err := run.CombinedOutput()
			if err != nil || string(out) != "stub "+version+"\n" {
				t.Fatalf("launcher run: %v %q", err, out)
			}
			got, _ := os.Readlink(link)
			if want := filepath.Join(home, "bin", c.want, name); got != want {
				t.Fatalf("link -> %s, want %s", got, want)
			}
		})
	}
}
