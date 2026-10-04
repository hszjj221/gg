package cliapp

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
)

const artifactUsage = `usage:
  gg artifact list
  gg artifact show <id>
  gg artifact publish <id>
  gg artifact remove <id>`

// runArtifactCommand implements `gg artifact ...`: inspect and publish
// agent-produced deliverables.
func runArtifactCommand(cfg config.Config, artifactArgs []string, stdout, stderr io.Writer) int {
	if len(artifactArgs) == 0 {
		fmt.Fprintln(stdout, artifactUsage)
		return 2
	}
	store, err := artifact.Open(cfg.Artifacts.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch artifactArgs[0] {
	case "list":
		return runArtifactList(store, stdout)
	case "show":
		return runArtifactShow(store, artifactArgs[1:], stdout, stderr)
	case "publish":
		return runArtifactPublish(cfg, store, artifactArgs[1:], stdout, stderr)
	case "remove":
		return runArtifactRemove(store, artifactArgs[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, artifactUsage)
		return 2
	}
}

func runArtifactList(store *artifact.Store, stdout io.Writer) int {
	items, err := store.List()
	if err != nil {
		fmt.Fprintln(stdout, err)
		return 1
	}
	if len(items) == 0 {
		fmt.Fprintln(stdout, "no artifacts")
		return 0
	}
	fmt.Fprintf(stdout, "%-10s %-30s %-8s %-4s %-4s %s\n", "ID", "TITLE", "TYPE", "VER", "PUB", "UPDATED")
	for _, a := range items {
		pub := "-"
		if a.PublishedVersion > 0 {
			pub = fmt.Sprintf("v%d", a.PublishedVersion)
		}
		fmt.Fprintf(stdout, "%-10s %-30s %-8s v%-3d %-4s %s\n",
			a.ID, truncate(a.Title, 30), a.Type, a.Version, pub,
			a.UpdatedAt.Format("2006-01-02 15:04"))
	}
	return 0
}

func runArtifactShow(store *artifact.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg artifact show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg artifact show <id>")
		return 2
	}
	a, err := store.Get(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	content, err := store.ReadVersion(a.ID, a.Version)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	pub := "unpublished"
	if a.PublishedVersion > 0 {
		pub = fmt.Sprintf("v%d", a.PublishedVersion)
	}
	fmt.Fprintf(stdout, "id: %s\ntitle: %s\ntype: %s\nversion: v%d\npublished: %s\nupdated: %s\n\n%s\n",
		a.ID, a.Title, a.Type, a.Version, pub,
		a.UpdatedAt.Format(time.RFC3339), content)
	return 0
}

// runArtifactPublish marks the latest version published and saves a copy of
// that version into the library, so agent-generated files land in the user's
// collection.
func runArtifactPublish(cfg config.Config, store *artifact.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg artifact publish", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg artifact publish <id>")
		return 2
	}
	lib, err := library.Open(cfg.Library.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result, err := app.PublishArtifact(store, lib, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "published %s v%d -> library %s\n", result.ID, result.PublishedVersion, result.LibraryName)
	return 0
}

func runArtifactRemove(store *artifact.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg artifact remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg artifact remove <id>")
		return 2
	}
	if err := store.Remove(fs.Arg(0)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "removed artifact %s\n", fs.Arg(0))
	return 0
}

func truncate(s string, n int) string {
	// Count runes, not bytes, so multibyte titles are never split mid-sequence.
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
