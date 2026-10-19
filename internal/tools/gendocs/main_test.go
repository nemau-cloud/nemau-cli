package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// committed is docs/commands, seen from this package's directory.
var committed = filepath.Join("..", "..", "..", "docs", "commands")

// TestReferenceIsUpToDate fails when a command, a flag or a help text changed
// without the reference being regenerated.
func TestReferenceIsUpToDate(t *testing.T) {
	want := pages(rootCommand())

	files, err := filepath.Glob(filepath.Join(committed, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, path := range files {
		name := filepath.Base(path)
		if _, ok := want[name]; !ok {
			stale = append(stale, name+" (no such command)")
		}
	}
	for name, content := range want {
		got, err := os.ReadFile(filepath.Join(committed, name))
		switch {
		case err != nil:
			stale = append(stale, name+" (missing)")
		case string(got) != content:
			stale = append(stale, name+" (differs)")
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("docs/commands is out of date; run `go run ./internal/tools/gendocs` from the repository root:\n%v", stale)
	}
}

func TestProseKeepsCommandsInCodeBlocks(t *testing.T) {
	got := prose("Fetch it.\n\n    nemau task wait <id>\n\nThen run <cmd> again.")
	want := "Fetch it.\n\n```\nnemau task wait <id>\n```\n\nThen run `<cmd>` again.\n\n"
	if got != want {
		t.Fatalf("prose:\n%q\nwant:\n%q", got, want)
	}
}
