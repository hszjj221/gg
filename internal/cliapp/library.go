package cliapp

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
)

const libraryUsage = `usage:
  gg library add <path> [--name NAME]
  gg library list
  gg library remove <id|name>
  gg library path <id|name>`

// runLibraryCommand implements `gg library ...`: manage the user's file
// collection (~/.gg/library).
func runLibraryCommand(cfg config.Config, libraryArgs []string, stdout, stderr io.Writer) int {
	if len(libraryArgs) == 0 {
		fmt.Fprintln(stdout, libraryUsage)
		return 2
	}
	store, err := library.Open(cfg.Library.Dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch libraryArgs[0] {
	case "add":
		return runLibraryAdd(store, libraryArgs[1:], stdout, stderr)
	case "list":
		return runLibraryList(store, stdout)
	case "remove":
		return runLibraryRemove(store, libraryArgs[1:], stdout, stderr)
	case "path":
		return runLibraryPath(store, libraryArgs[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, libraryUsage)
		return 2
	}
}

func runLibraryAdd(store *library.Store, args []string, stdout, stderr io.Writer) int {
	// Accept --name in any position: Go's flag package stops parsing at the
	// first positional argument, so `gg library add <path> --name NAME` (the
	// documented form) would otherwise fail.
	name, rest := extractNameFlag(args)
	fs := flag.NewFlagSet("gg library add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg library add <path> [--name NAME]")
		return 2
	}
	entry, err := store.Add(fs.Arg(0), name, library.SourceUpload)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "added %s (%d bytes)\n", entry.Name, entry.Size)
	return 0
}

// extractNameFlag pulls --name/-name out of args regardless of position,
// supporting both "--name VALUE" and "--name=VALUE" forms.
func extractNameFlag(args []string) (string, []string) {
	var name string
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if v, ok := strings.CutPrefix(a, "--name="); ok {
			name = v
			continue
		}
		if v, ok := strings.CutPrefix(a, "-name="); ok {
			name = v
			continue
		}
		if a == "--name" || a == "-name" {
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return name, rest
}

func runLibraryList(store *library.Store, stdout io.Writer) int {
	entries, err := store.List()
	if err != nil {
		fmt.Fprintln(stdout, err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "library is empty")
		return 0
	}
	fmt.Fprintf(stdout, "%-10s %-28s %-10s %-16s %s\n", "ID", "NAME", "SIZE", "SOURCE", "ADDED")
	for _, e := range entries {
		fmt.Fprintf(stdout, "%-10s %-28s %-10d %-16s %s\n",
			e.ID, truncate(e.Name, 28), e.Size, truncate(e.Source, 16),
			e.AddedAt.Format("2006-01-02 15:04"))
	}
	return 0
}

func runLibraryRemove(store *library.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg library remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg library remove <id|name>")
		return 2
	}
	if err := store.Remove(fs.Arg(0)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "removed %s\n", fs.Arg(0))
	return 0
}

func runLibraryPath(store *library.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg library path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: gg library path <id|name>")
		return 2
	}
	_, path, err := store.Resolve(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}
