package jsonrpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/workspace"
)

func testRegistry(t *testing.T) *workspace.Registry {
	t.Helper()
	reg, err := workspace.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func testWorkspaceDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func testArtifactHandler(t *testing.T) (*Handler, *artifact.Store) {
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
	rt, err := app.NewRuntime(app.RuntimeOptions{
		Config:            config.Config{CWD: testWorkspaceDir(t, root, "project"), Selection: "test:model"},
		ProviderFactory:   func(config.Config) agent.Provider { return fakeProvider{} },
		WorkspaceRegistry: testRegistry(t),
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
		ArtifactStore:     astore,
		LibraryStore:      lstore,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(rt), astore
}

func callMethod(t *testing.T, h *Handler, method, params string) Response {
	t.Helper()
	return h.Handle(context.Background(), Request{
		JSONRPC: Version, ID: []byte(`1`), Method: method, Params: []byte(params),
	})
}

func TestArtifactRPCFlow(t *testing.T) {
	h, astore := testArtifactHandler(t)
	a, err := astore.Create("Trip", artifact.TypeMarkdown, "# v1")
	if err != nil {
		t.Fatal(err)
	}

	listed := callMethod(t, h, "artifact.list", "")
	if listed.Error != nil {
		t.Fatal(listed.Error)
	}
	items, ok := listed.Result.([]*artifact.Artifact)
	if !ok || len(items) != 1 || items[0].ID != a.ID {
		t.Fatalf("artifact.list = %#v", listed.Result)
	}

	got := callMethod(t, h, "artifact.get", `{"artifactId":"`+a.ID+`"}`)
	if got.Error != nil {
		t.Fatal(got.Error)
	}
	view, ok := got.Result.(app.ArtifactView)
	if !ok || view.Content != "# v1" || view.Meta.Title != "Trip" {
		t.Fatalf("artifact.get = %#v", got.Result)
	}

	published := callMethod(t, h, "artifact.publish", `{"artifactId":"`+a.ID+`"}`)
	if published.Error != nil {
		t.Fatal(published.Error)
	}
	res, ok := published.Result.(app.PublishResult)
	if !ok || res.PublishedVersion != 1 || res.LibraryName != "trip.md" {
		t.Fatalf("artifact.publish = %#v", published.Result)
	}

	// Missing id is a parameter error, not a method error.
	bad := callMethod(t, h, "artifact.get", `{}`)
	if bad.Error == nil || bad.Error.Code != -32602 {
		t.Fatalf("artifact.get without id = %+v", bad.Error)
	}
	// Unknown id maps to a JSON-RPC error (not a crash).
	missing := callMethod(t, h, "artifact.get", `{"artifactId":"nope"}`)
	if missing.Error == nil {
		t.Fatal("expected error for unknown artifact")
	}
	data, _ := json.Marshal(missing)
	if !containsJSONKey(data, "code") || !strings.Contains(string(data), "artifact_not_found") {
		t.Fatalf("missing artifact error = %s", data)
	}
}
