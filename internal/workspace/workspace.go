// Package workspace manages gg's named project roots ("workspaces").
//
// A workspace is a registered local directory that gg is allowed to work in.
// The registry lives at ~/.gg/workspaces.json (0600). In P1 the registry is
// only populated — the process CWD is auto-registered as the "default"
// workspace — while the runtime still serves a single root exactly like
// before. Later phases bind sessions to workspaces and serve several roots
// from one daemon.
package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultName is the name of the workspace auto-registered for the process
// working directory on startup.
const DefaultName = "default"

// registryVersion is the on-disk layout version of workspaces.json.
const registryVersion = 1

// registryFileName is the registry file name inside the ~/.gg directory.
const registryFileName = "workspaces.json"

// Workspace is a named, registered project root.
type Workspace struct {
	// ID is the stable identifier, e.g. "w_3f9a1c2e4b5d6e7f".
	ID string `json:"id"`
	// Name is the human-friendly label, unique across the registry.
	Name string `json:"name"`
	// Root is the canonical absolute path of the project directory.
	Root string `json:"root"`
	// CreatedAt is when the workspace was registered.
	CreatedAt time.Time `json:"createdAt"`
}

// newID generates a stable workspace identifier.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is effectively impossible; fall back to a
		// timestamp-based ID rather than failing registration.
		return fmt.Sprintf("w_%d", time.Now().UnixNano())
	}
	return "w_" + hex.EncodeToString(b[:])
}

// canonicalRoot validates a candidate workspace root and returns its canonical
// form: absolute, symlinks resolved. The root must exist and be a directory.
func canonicalRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("workspace root %q is not absolute", root)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("workspace root %q cannot be resolved: %w", root, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("workspace root %q is not accessible: %w", canonical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace root %q is not a directory", canonical)
	}
	return canonical, nil
}

// RegistryPath returns the registry file path for a home directory.
func RegistryPath(homeDir string) string {
	return filepath.Join(homeDir, ".gg", registryFileName)
}
