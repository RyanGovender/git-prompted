package core

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Scan limits keep `topics list --repo` quick on large repositories. The result is a
// hint for onboarding, so a capped sample is good enough.
const (
	scanMaxFiles    = 2000
	scanMaxFileSize = 128 << 10 // bigger files are usually generated or minified
	scanMaxBytes    = 8 << 20
	scanMaxTime     = 3 * time.Second
)

// scanSkipDirs are dependency, build and tool directories: code the user did not write.
var scanSkipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"out": true, "bin": true, "obj": true, "venv": true, "__pycache__": true, "Pods": true,
}

// ScanRepo counts, for each topic, how many files under dir it would fire on, using
// the same matching as checkpoints (so prose files are skipped). Hidden directories,
// dependency/build directories, binaries and files over 128 KB are skipped, and the
// walk stops after 2000 files, 8 MB of code or 3 seconds, whichever comes first.
// Topics must already be validated.
func ScanRepo(dir string, topics []*Topic) (map[string]int, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	for _, t := range topics {
		t.compile(func(string) {})
	}
	counts := map[string]int{}
	seen, total := 0, 0
	deadline := time.Now().Add(scanMaxTime)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil // unreadable entry: skip it, keep scanning
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (name[0] == '.' || scanSkipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || name[0] == '.' {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > scanMaxFileSize {
			return nil
		}
		if seen >= scanMaxFiles || total >= scanMaxBytes || time.Now().After(deadline) {
			return filepath.SkipAll
		}
		raw, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(raw[:min(len(raw), 8000)], 0) >= 0 {
			return nil
		}
		seen++
		total += len(raw)
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		files, code := []string{filepath.ToSlash(rel)}, string(raw)
		for _, t := range topics {
			if t.Matches(files, code) {
				counts[t.ID]++
			}
		}
		return nil
	})
	return counts, err
}
