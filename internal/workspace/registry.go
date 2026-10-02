package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/filelock"
)

// registryFile is the on-disk shape of workspaces.json.
type registryFile struct {
	Version    int         `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
}

// Registry is the in-memory view of ~/.gg/workspaces.json. It is not safe
// for concurrent use; the daemon is single-instance (pidfile flock), and the
// CLI is single-shot, so no locking is needed.
type Registry struct {
	path       string
	workspaces []Workspace
}

// Load reads the registry for homeDir. A missing file yields an empty
// registry; a corrupt file or an unknown layout version is an error.
func Load(homeDir string) (*Registry, error) {
	path := RegistryPath(homeDir)
	r := &Registry{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace registry: %w", err)
	}
	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse workspace registry %q: %w", path, err)
	}
	if file.Version != registryVersion {
		return nil, fmt.Errorf("workspace registry %q has unsupported version %d", path, file.Version)
	}
	r.workspaces = file.Workspaces
	return r, nil
}

// Save persists the registry atomically (temp file + rename) with 0600
// permissions. The ~/.gg directory is created 0700 if missing.
func (r *Registry) Save() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("create gg home directory: %w", err)
	}
	data, err := json.MarshalIndent(registryFile{
		Version:    registryVersion,
		Workspaces: append([]Workspace{}, r.workspaces...),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode workspace registry: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), "workspaces-*.json")
	if err != nil {
		return fmt.Errorf("write workspace registry: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write workspace registry: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write workspace registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write workspace registry: %w", err)
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write workspace registry: %w", err)
	}
	return nil
}

// Path returns the registry file path.
func (r *Registry) Path() string { return r.path }

// List returns all registered workspaces in registration order.
func (r *Registry) List() []Workspace {
	out := make([]Workspace, len(r.workspaces))
	copy(out, r.workspaces)
	return out
}

// FindByName returns the workspace with the given name.
func (r *Registry) FindByName(name string) (Workspace, bool) {
	for _, w := range r.workspaces {
		if w.Name == name {
			return w, true
		}
	}
	return Workspace{}, false
}

// FindByID returns the workspace with the given stable ID.
func (r *Registry) FindByID(id string) (Workspace, bool) {
	for _, w := range r.workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return Workspace{}, false
}

// FindByRoot returns the workspace whose root names the same directory as
// root. The input is canonicalized the same way as at registration;
// unresolvable paths simply miss. Comparison is by filesystem identity, not
// string equality: on case-insensitive filesystems (default macOS)
// differently-spelled paths can name the same directory.
func (r *Registry) FindByRoot(root string) (Workspace, bool) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, false
	}
	for _, w := range r.workspaces {
		if sameRoot(w.Root, canonical) {
			return w, true
		}
	}
	return Workspace{}, false
}

// sameRoot reports whether a and b name the same directory. The string
// fast path covers the common case; the os.SameFile fallback handles
// filesystems where different spellings resolve to one directory.
func sameRoot(a, b string) bool {
	if a == b {
		return true
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// Add registers a new workspace. The change is staged in memory; call Save
// to persist it. The name must be unique; the root must be an existing
// directory and must not already be registered under another name.
func (r *Registry) Add(name, root string) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, fmt.Errorf("workspace name is required")
	}
	if _, ok := r.FindByName(name); ok {
		return Workspace{}, fmt.Errorf("workspace %q already exists", name)
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return Workspace{}, err
	}
	for _, w := range r.workspaces {
		if sameRoot(w.Root, canonical) {
			return Workspace{}, fmt.Errorf("root %q is already registered as workspace %q", canonical, w.Name)
		}
	}
	w := Workspace{
		ID:        newID(),
		Name:      name,
		Root:      canonical,
		CreatedAt: time.Now().UTC(),
	}
	r.workspaces = append(r.workspaces, w)
	return w, nil
}

// Remove deletes a workspace by name or ID. The change is staged in memory;
// call Save to persist it. It never touches the directory or the
// workspace's sessions: re-registering the same root later keeps working
// (matched by canonical root).
func (r *Registry) Remove(nameOrID string) error {
	for i, w := range r.workspaces {
		if w.Name == nameOrID || w.ID == nameOrID {
			r.workspaces = append(r.workspaces[:i], r.workspaces[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("workspace %q not found", nameOrID)
}

// EnsureDefault makes sure root is registered, returning the workspace and
// whether it was newly added. If root is already registered under any name,
// that workspace is returned. Otherwise it is registered under the
// "default" name when free, or under a derived name (the directory base
// name, deduplicated) when "default" is taken by another root: silently
// adopting the foreign default would point this process at the wrong
// project (e.g. `ggd --cwd /b` operating on /a's sessions and tools).
func (r *Registry) EnsureDefault(root string) (Workspace, bool, error) {
	if w, ok := r.FindByRoot(root); ok {
		return w, false, nil
	}
	name := DefaultName
	if _, ok := r.FindByName(name); ok {
		name = deriveWorkspaceName(r, root)
	}
	w, err := r.Add(name, root)
	if err != nil {
		return Workspace{}, false, err
	}
	return w, true, nil
}

// deriveWorkspaceName picks a registration name from the directory base
// name, suffixed until unique.
func deriveWorkspaceName(r *Registry, root string) string {
	base := filepath.Base(root)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "workspace"
	}
	name := base
	for i := 2; ; i++ {
		if _, ok := r.FindByName(name); !ok {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// EnsureDefaultWorkspace loads the registry for homeDir, ensures root is
// registered (see EnsureDefault), and saves only when something was added.
// It is the startup hook shared by the daemon and the CLI: after an upgrade,
// the first run registers the process working directory as "default" (or a
// derived name when "default" is taken) with zero user action.
//
// root may be relative (e.g. ggd --cwd .); it is resolved against the process
// working directory before registration, while the stored root stays
// canonical and absolute. The whole load-modify-save cycle is serialized
// across processes with an exclusive lock: the daemon's single-instance
// pidfile lock is acquired after this hook, and single-shot CLI processes
// can otherwise race each other into lost registrations.
func EnsureDefaultWorkspace(homeDir, root string) (Workspace, bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, false, fmt.Errorf("resolve workspace root: %w", err)
	}
	ggDir := filepath.Join(homeDir, ".gg")
	if err := os.MkdirAll(ggDir, 0o700); err != nil {
		return Workspace{}, false, fmt.Errorf("create gg home directory: %w", err)
	}
	unlock, err := filelock.Lock(filepath.Join(ggDir, "workspaces.lock"))
	if err != nil {
		return Workspace{}, false, fmt.Errorf("lock workspace registry: %w", err)
	}
	defer unlock()
	r, err := Load(homeDir)
	if err != nil {
		return Workspace{}, false, err
	}
	w, added, err := r.EnsureDefault(abs)
	if err != nil {
		return Workspace{}, false, err
	}
	if added {
		if err := r.Save(); err != nil {
			return Workspace{}, false, err
		}
	}
	return w, added, nil
}

// ResolveForSession maps a session to its workspace:
//  1. A non-empty workspaceID present in the registry wins: (ws, false, nil),
//     no backfill needed.
//  2. Otherwise the session's cwd is matched by canonical root (FindByRoot);
//     a hit returns (ws, true, nil) and the caller should backfill ws.ID
//     into the session header via session.Store.SetWorkspaceID.
//  3. Unknown workspaceID or unregistered root falls back to the "default"
//     workspace with (ws, true, nil); an error is returned when even the
//     default workspace is missing.
//
// backfill reports whether the caller should write ws.ID back into the
// session header.
func (r *Registry) ResolveForSession(workspaceID, cwd string) (Workspace, bool, error) {
	if workspaceID != "" {
		if w, ok := r.FindByID(workspaceID); ok {
			return w, false, nil
		}
	}
	if cwd != "" {
		abs, err := filepath.Abs(cwd)
		if err == nil {
			// FindByRoot canonicalizes via canonicalRoot and misses when the
			// cwd cannot be resolved (e.g. it no longer exists): skip root
			// matching in that case and fall through to default instead of
			// surfacing the error.
			if w, ok := r.FindByRoot(abs); ok {
				return w, true, nil
			}
		}
	}
	if w, ok := r.FindByName(DefaultName); ok {
		return w, true, nil
	}
	return Workspace{}, false, fmt.Errorf("no default workspace registered")
}
