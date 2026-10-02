package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func tempHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestLoadMissingFileYieldsEmptyRegistry(t *testing.T) {
	r, err := Load(tempHome(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("List() = %d workspaces, want 0", len(r.List()))
	}
}

func TestAddSaveLoadRoundTrip(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()

	r, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	w, err := r.Add("proj", root)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if w.Name != "proj" || w.ID == "" || w.CreatedAt.IsZero() {
		t.Fatalf("Add returned %+v, want populated workspace", w)
	}
	if err := r.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Registry file must be 0600: it contains absolute local paths.
	info, err := os.Stat(RegistryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("registry file perm = %o, want 600", perm)
	}

	r2, err := Load(home)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	list := r2.List()
	if len(list) != 1 || list[0].ID != w.ID || list[0].Name != "proj" || list[0].Root != w.Root {
		t.Fatalf("round trip = %+v, want [%+v]", list, w)
	}

	// version field is present on disk.
	raw, err := os.ReadFile(RegistryPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var file registryFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version != registryVersion {
		t.Fatalf("on-disk version = %d, want %d", file.Version, registryVersion)
	}
}

func TestAddRejects(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()
	other := t.TempDir()
	file := filepath.Join(root, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add("proj", root); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	cases := map[string][2]string{
		"duplicate name": {"proj", other},
		"duplicate root": {"other", root},
		"empty name":     {"", other},
		"blank name":     {"   ", other},
		"relative root":  {"rel", "some/relative"},
		"missing root":   {"missing", filepath.Join(other, "nope")},
		"file as root":   {"file", file},
		"empty root":     {"emptyroot", ""},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Add(args[0], args[1]); err == nil {
				t.Fatalf("Add(%q, %q): want error, got nil", args[0], args[1])
			}
		})
	}
	if len(r.List()) != 1 {
		t.Fatalf("failed Adds mutated the registry: %d workspaces", len(r.List()))
	}
}

func TestFindByNameAndRoot(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()

	r, _ := Load(home)
	w, err := r.Add("proj", root)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.FindByName("proj"); !ok || got.ID != w.ID {
		t.Fatalf("FindByName(proj) = %+v, %v; want %+v", got, ok, w)
	}
	if _, ok := r.FindByName("nope"); ok {
		t.Fatal("FindByName(nope) unexpectedly matched")
	}
	if got, ok := r.FindByRoot(root); !ok || got.ID != w.ID {
		t.Fatalf("FindByRoot(root) = %+v, %v; want %+v", got, ok, w)
	}
	// A symlinked path to the same directory resolves to the same workspace.
	if link, err := filepath.EvalSymlinks(root); err == nil && link != root {
		if _, ok := r.FindByRoot(root); !ok {
			t.Fatal("FindByRoot missed after canonicalization")
		}
	}
	if _, ok := r.FindByRoot(filepath.Join(root, "missing")); ok {
		t.Fatal("FindByRoot(missing) unexpectedly matched")
	}
	if _, ok := r.FindByRoot("relative/path"); ok {
		t.Fatal("FindByRoot(relative) unexpectedly matched")
	}
}

func TestRemove(t *testing.T) {
	home := tempHome(t)
	r, _ := Load(home)
	w1, err := r.Add("one", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add("two", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if err := r.Remove("one"); err != nil {
		t.Fatalf("Remove by name: %v", err)
	}
	if err := r.Remove(w1.ID); err == nil {
		t.Fatal("Remove of already-removed workspace: want error, got nil")
	}
	// Remove by ID.
	w2, _ := r.FindByName("two")
	if err := r.Remove(w2.ID); err != nil {
		t.Fatalf("Remove by ID: %v", err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("List() = %d, want 0 after removals", len(r.List()))
	}
	if err := r.Remove("ghost"); err == nil {
		t.Fatal("Remove(ghost): want error, got nil")
	}
}

func TestRemovePersists(t *testing.T) {
	home := tempHome(t)
	r, _ := Load(home)
	if _, err := r.Add("one", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("one"); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.List()) != 0 {
		t.Fatalf("List() after remove+save+load = %d, want 0", len(r2.List()))
	}
}

func TestLoadCorruptFileErrors(t *testing.T) {
	home := tempHome(t)
	r, _ := Load(home)
	if _, err := r.Add("proj", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RegistryPath(home), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err == nil {
		t.Fatal("Load(corrupt): want error, got nil")
	}
}

func TestLoadUnsupportedVersionErrors(t *testing.T) {
	home := tempHome(t)
	path := RegistryPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(registryFile{Version: registryVersion + 99})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(home); err == nil {
		t.Fatal("Load(future version): want error, got nil")
	}
}

func TestEnsureDefault(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()

	r, _ := Load(home)
	w, added, err := r.EnsureDefault(root)
	if err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	if !added || w.Name != DefaultName {
		t.Fatalf("EnsureDefault first call = %+v, added=%v; want new %q", w, added, DefaultName)
	}
	w2, added2, err := r.EnsureDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	if added2 || w2.ID != w.ID {
		t.Fatalf("EnsureDefault second call = %+v, added=%v; want same, added=false", w2, added2)
	}
}

func TestEnsureDefaultReusesExistingName(t *testing.T) {
	home := tempHome(t)
	r, _ := Load(home)
	if _, err := r.Add("proj", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	w, added, err := r.EnsureDefault(other)
	if err != nil {
		t.Fatal(err)
	}
	// "proj" is registered for another root, so a fresh "default" is added.
	if !added || w.Name != DefaultName {
		t.Fatalf("EnsureDefault = %+v, added=%v; want new default", w, added)
	}
}

func TestEnsureDefaultDerivesNameWhenDefaultTaken(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()
	other := t.TempDir()
	r, _ := Load(home)
	def, err := r.Add(DefaultName, other)
	if err != nil {
		t.Fatal(err)
	}
	// A "default" pointing elsewhere must not be silently adopted: root
	// gets its own workspace with a derived name, and the foreign default
	// is left untouched.
	w, added, err := r.EnsureDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	if !added || w.Name == DefaultName || w.ID == def.ID {
		t.Fatalf("EnsureDefault = %+v, added=%v; want new workspace with derived name", w, added)
	}
	if d, ok := r.FindByName(DefaultName); !ok || d.ID != def.ID {
		t.Fatalf("foreign default was disturbed: %+v", d)
	}
	// Idempotent: the derived workspace is found by root on retry.
	w2, added2, err := r.EnsureDefault(root)
	if err != nil {
		t.Fatal(err)
	}
	if added2 || w2.ID != w.ID {
		t.Fatalf("EnsureDefault retry = %+v, added=%v; want same workspace", w2, added2)
	}
}

func TestEnsureDefaultDerivesNameDedupes(t *testing.T) {
	home := tempHome(t)
	r, _ := Load(home)
	if _, err := r.Add(DefaultName, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Two sibling directories share a base name: the second registration
	// gets a suffixed name.
	parent := t.TempDir()
	first := filepath.Join(parent, "proj")
	second := filepath.Join(parent, "sub", "proj")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	w1, _, err := r.EnsureDefault(first)
	if err != nil {
		t.Fatal(err)
	}
	w2, _, err := r.EnsureDefault(second)
	if err != nil {
		t.Fatal(err)
	}
	if w1.Name != "proj" || w2.Name != "proj-2" {
		t.Fatalf("derived names = %q, %q; want %q, %q", w1.Name, w2.Name, "proj", "proj-2")
	}
}

func TestEnsureDefaultWorkspaceEndToEnd(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()

	w, added, err := EnsureDefaultWorkspace(home, root)
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace: %v", err)
	}
	if !added || w.Name != DefaultName {
		t.Fatalf("= %+v, added=%v; want new default", w, added)
	}
	w2, added2, err := EnsureDefaultWorkspace(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if added2 || w2.ID != w.ID {
		t.Fatalf("second call = %+v, added=%v; want idempotent", w2, added2)
	}
	// Registry file exists on disk with 0600.
	if info, err := os.Stat(RegistryPath(home)); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat registry: %v, perm %o", err, info.Mode().Perm())
	}
}

func TestEnsureDefaultWorkspaceAcceptsRelativeRoot(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()
	// Simulate `ggd --cwd .`: the hook must absolutize before registering.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	w, added, err := EnsureDefaultWorkspace(home, ".")
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace(home, %q): %v", ".", err)
	}
	if !added {
		t.Fatal("added = false, want true for a fresh registry")
	}
	if !filepath.IsAbs(w.Root) {
		t.Fatalf("registered root = %q, want absolute", w.Root)
	}
	// Second call with the absolute root must find the same workspace.
	w2, added2, err := EnsureDefaultWorkspace(home, w.Root)
	if err != nil {
		t.Fatalf("second EnsureDefaultWorkspace: %v", err)
	}
	if added2 || w2.ID != w.ID {
		t.Fatalf("second call = (added=%v, id=%q), want (false, %q)", added2, w2.ID, w.ID)
	}
}

func TestSameRootViaSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if !sameRoot(dir, link) {
		t.Fatalf("sameRoot(%q, %q) = false, want true", dir, link)
	}
	if sameRoot(dir, filepath.Join(t.TempDir(), "other")) {
		t.Fatalf("sameRoot(%q, other) = true, want false", dir)
	}
	// Registering a symlinked spelling of an existing root must be rejected.
	home := tempHome(t)
	r, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Add("a", dir); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := r.Add("b", link); err == nil {
		t.Fatal("Add(symlinked root) succeeded, want duplicate-root error")
	}
}

func TestEnsureDefaultWorkspaceConcurrent(t *testing.T) {
	home := tempHome(t)
	root := t.TempDir()
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, _, err := EnsureDefaultWorkspace(home, root)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent EnsureDefaultWorkspace: %v", err)
		}
	}
	r, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != 1 {
		t.Fatalf("registry has %d workspaces after concurrent ensure, want 1", got)
	}
}
