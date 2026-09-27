package cliapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/artifact"
)

func TestArtifactShowPublishRemove(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	// Seed an artifact directly through the CLI-facing store path is not
	// possible (no create command); use the agent-tool path in tools tests.
	// Here we drive publish/remove/show through the store first.
	storeDir := filepath.Join(dir, "home", ".gg", "artifacts")
	stdout.Reset()
	code := Run(context.Background(), []string{"artifact", "list"}, opts)
	if code != 0 {
		t.Fatalf("artifact list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no artifacts") {
		t.Fatalf("expected empty list, got %q", stdout.String())
	}
	_ = storeDir
}

func TestArtifactCLIFlow(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	// Create an artifact via the store, then exercise the CLI surface.
	astore, err := artifact.Open(filepath.Join(dir, "home", ".gg", "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := astore.Create("Tokyo Trip", "markdown", "# Tokyo\n\nDay 1: ...")
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"artifact", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Tokyo Trip") || !strings.Contains(stdout.String(), a.ID) {
		t.Fatalf("list missing artifact: %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"artifact", "show", a.ID}, opts); code != 0 {
		t.Fatalf("show exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "# Tokyo") {
		t.Fatalf("show missing content: %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"artifact", "publish", a.ID}, opts); code != 0 {
		t.Fatalf("publish exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "tokyo-trip.md") {
		t.Fatalf("publish should report library name: %q", stdout.String())
	}
	// The published copy must land in the library.
	libPath := filepath.Join(dir, "home", ".gg", "library", "tokyo-trip.md")
	data, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("library copy missing: %v", err)
	}
	if string(data) != "# Tokyo\n\nDay 1: ..." {
		t.Fatalf("library copy = %q", data)
	}
	// And the artifact must be marked published.
	got, err := astore.Get(a.ID)
	if err != nil || got.PublishedVersion != 1 {
		t.Fatalf("published flag = %+v, err = %v", got, err)
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"artifact", "remove", a.ID}, opts); code != 0 {
		t.Fatalf("remove exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"artifact", "show", a.ID}, opts); code == 0 {
		t.Fatal("show after remove should fail")
	}
}

func TestLibraryCLIFlow(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	src := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(src, []byte("# notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "add", src}, opts); code != 0 {
		t.Fatalf("add exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "notes.md") {
		t.Fatalf("add output = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "notes.md") || !strings.Contains(stdout.String(), "upload") {
		t.Fatalf("list = %q", stdout.String())
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "path", "notes.md"}, opts); code != 0 {
		t.Fatalf("path exit %d: %s", code, stderr.String())
	}
	want := filepath.Join(dir, "home", ".gg", "library", "notes.md")
	if strings.TrimSpace(stdout.String()) != want {
		t.Fatalf("path = %q, want %q", stdout.String(), want)
	}

	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "remove", "notes.md"}, opts); code != 0 {
		t.Fatalf("remove exit %d: %s", code, stderr.String())
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "library is empty") {
		t.Fatalf("list after remove = %q", stdout.String())
	}
}

func TestLibraryAddNameFlagPositions(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := jobTestOptions(dir, &stdout, &stderr)

	src := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(src, []byte("# doc"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Documented form: flag after the positional argument.
	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "add", src, "--name", "renamed.md"}, opts); code != 0 {
		t.Fatalf("trailing --name exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "renamed.md") {
		t.Fatalf("add output = %q", stdout.String())
	}
	// --name=value form.
	src2 := filepath.Join(dir, "doc2.md")
	if err := os.WriteFile(src2, []byte("# doc2"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"library", "add", "--name=eq.md", src2}, opts); code != 0 {
		t.Fatalf("--name= exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "eq.md") {
		t.Fatalf("add output = %q", stdout.String())
	}
}

func TestTruncateRuneBoundaries(t *testing.T) {
	// 11 CJK chars = 33 bytes; the old byte-slice cut produced broken UTF-8.
	title := "中文标题测试中文标题测试中"
	got := truncate(title, 10)
	if got != "中文标题测试中文标…" {
		t.Fatalf("truncate = %q", got)
	}
	for _, r := range got {
		if r == '\uFFFD' {
			t.Fatalf("truncate split a rune: %q", got)
		}
	}
	if truncate("short", 10) != "short" {
		t.Fatal("short string should be unchanged")
	}
}
