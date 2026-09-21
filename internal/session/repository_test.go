package session

import (
	"path/filepath"
	"testing"
)

func TestFileRepositoryUsesIDsAtApplicationBoundary(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	repository := NewFileRepository(filepath.Join(root, "sessions"))
	store, loaded, err := repository.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.ID == "" || store.Path() == "" {
		t.Fatalf("created session is incomplete: store=%+v loaded=%+v", store, loaded)
	}
	opened, reopened, err := repository.OpenForCWD(cwd, loaded.Header.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Path() != store.Path() || reopened.Header.ID != loaded.Header.ID {
		t.Fatalf("opened wrong session: path=%q header=%+v", opened.Path(), reopened.Header)
	}
	if _, _, err := repository.OpenForCWD(cwd, store.Path(), false); err == nil {
		t.Fatal("repository accepted a path at the ID-only boundary")
	}
}
