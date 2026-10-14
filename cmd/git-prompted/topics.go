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

const topicsUsage = "usage: git-prompted topics list [--json] [--repo DIR] | add <id>... | add --json - | remove <id>..."

// runTopics lists the catalog and adds or removes topics in goals.json.
// Agents run it during onboarding; every write is validated by core. A non-empty
// repo makes list count matching files in that directory.
func runTopics(w io.Writer, in io.Reader, args []string, asJSON bool, repo string) error {
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
		if repo != "" {
			list, err = core.ListTopicsForRepo(catalog, repo)
		}
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
			seen := ""
			if n := l.SeenInRepo; n != nil && *n == 1 {
				seen = "  (1 file here)"
			} else if n != nil && *n > 1 {
				seen = fmt.Sprintf("  (%d files here)", *n)
			}
			fmt.Fprintf(w, "%s %-16s %-11s %s%s\n", mark, l.ID, l.Mode, l.Name, seen)
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
