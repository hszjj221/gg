package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
	"github.com/hszjj221/gg/internal/session"
)

func testArtifactWorkspace(t *testing.T) (*Workspace, *artifact.Store) {
	t.Helper()
	root := t.TempDir()
	astore, err := artifact.Open(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	lstore, err := library.Open(filepath.Join(root, "library"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(WorkspaceOptions{
		Config:          config.Config{CWD: filepath.Join(root, "project"), Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return &runtimeProvider{} },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
		ArtifactStore:   astore,
		LibraryStore:    lstore,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, astore
}

func TestWorkspaceArtifactFlow(t *testing.T) {
	w, astore := testArtifactWorkspace(t)

	a, err := astore.Create("Trip", artifact.TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := astore.AddVersion(a.ID, "# v2"); err != nil {
		t.Fatal(err)
	}

	list, err := w.ListArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list = %+v", list)
	}

	// Unpublished: the reader sees the latest draft.
	view, err := w.GetArtifact(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != 2 || view.PublishedVersion != 0 || view.Content != "# v2" {
		t.Fatalf("view = %+v", view)
	}

	res, err := w.PublishArtifact(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PublishedVersion != 2 || res.LibraryName != "trip.md" {
		t.Fatalf("publish = %+v", res)
	}
	// Library copy landed.
	data, err := os.ReadFile(filepath.Join(w.libraryStore.Dir(), "trip.md"))
	if err != nil || string(data) != "# v2" {
		t.Fatalf("library copy = %q, err = %v", data, err)
	}
	// Published: the reader sees the published version.
	view, err = w.GetArtifact(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.PublishedVersion != 2 || view.Content != "# v2" {
		t.Fatalf("published view = %+v", view)
	}
	// A newer draft stays visible after publishing: the reader shows the
	// latest version, with the published marker intact.
	if _, err := astore.AddVersion(a.ID, "# v3 draft"); err != nil {
		t.Fatal(err)
	}
	view, err = w.GetArtifact(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != 3 || view.PublishedVersion != 2 || view.Content != "# v3 draft" {
		t.Fatalf("draft view = %+v", view)
	}
	// Publishing the draft marks v3 and saves v3's bytes as a new library
	// entry (the v2 copy stays untouched).
	res, err = w.PublishArtifact(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.PublishedVersion != 3 || res.LibraryName != "trip-2.md" {
		t.Fatalf("republish = %+v", res)
	}
	data, err = os.ReadFile(filepath.Join(w.libraryStore.Dir(), "trip-2.md"))
	if err != nil || string(data) != "# v3 draft" {
		t.Fatalf("library copy after republish = %q, err = %v", data, err)
	}
}

func TestWorkspaceArtifactNotFound(t *testing.T) {
	w, _ := testArtifactWorkspace(t)
	if _, err := w.GetArtifact("nope"); !isArtifactNotFound(err) {
		t.Fatalf("get: expected artifact_not_found, got %v", err)
	}
	if _, err := w.PublishArtifact("nope"); !isArtifactNotFound(err) {
		t.Fatalf("publish: expected artifact_not_found, got %v", err)
	}
}

func isArtifactNotFound(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Code == ErrorArtifactNotFound
}

func TestWorkspaceArtifactStoreUnavailable(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(WorkspaceOptions{
		Config:          config.Config{CWD: filepath.Join(root, "project"), Selection: "test:model"},
		ProviderFactory: func(config.Config) agent.Provider { return &runtimeProvider{} },
		Repository:      session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ListArtifacts(); err == nil {
		t.Fatal("expected error without artifact store")
	}
}
