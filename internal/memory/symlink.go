package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Symlink handling for workspace memory layers.
//
// A workspace directory can come from anywhere — including a freshly
// cloned, untrusted repository. Its .gg/memory tree must therefore be
// treated as attacker-controlled: a symlinked MEMORY.md pointing at
// ~/.ssh/id_rsa would otherwise have its target's contents read into the
// prompt sent to the provider (exfiltration), and appends would corrupt
// external files without approval. The global store (~/.gg/memory) keeps
// the historical follow-symlinks behavior: it is the user's own
// directory, and some users intentionally symlink it elsewhere.
//
// The rules, enforced for every workspace-layer file access:
//   - the layer directory itself must not be a symlink;
//   - the accessed file itself must not be a symlink;
//   - the fully resolved path must stay inside the resolved layer
//     directory (catches symlinked parent directories like .gg).

// resolveWorkspaceDir validates the workspace layer directory and returns
// its real (symlink-resolved) path. A symlinked layer directory is
// rejected outright.
func resolveWorkspaceDir(layerDir string) (string, error) {
	fi, err := os.Lstat(layerDir)
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace memory directory %q is a symlink", layerDir)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("workspace memory directory %q is not a directory", layerDir)
	}
	return filepath.EvalSymlinks(layerDir)
}

// resolveWorkspaceFile resolves path for I/O against the workspace layer
// rooted at layerDir, failing closed on symlink escapes. It returns the
// real path the caller must use for the actual I/O, so no symlink is left
// to be followed after the check.
//
// The layer directory may not exist yet (it is created lazily on first
// write); in that case the nearest existing ancestor anchors the
// containment check.
func resolveWorkspaceFile(layerDir, path string) (string, error) {
	realBase, err := workspaceRealBase(layerDir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("workspace memory file %q is a symlink", path)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	real, err := realPathBestEffort(path)
	if err != nil {
		return "", err
	}
	if !withinDir(realBase, real) {
		return "", fmt.Errorf("workspace memory path %q escapes its directory", path)
	}
	return real, nil
}

// workspaceRealBase returns the symlink-resolved workspace layer
// directory, rejecting a symlinked layer directory. When the layer does
// not exist yet, the nearest existing ancestor anchors the result.
func workspaceRealBase(layerDir string) (string, error) {
	if fi, err := os.Lstat(layerDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("workspace memory directory %q is a symlink", layerDir)
		}
		return filepath.EvalSymlinks(layerDir)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Not created yet: resolve the nearest existing ancestor and
	// re-append the missing tail.
	var tail []string
	cur := layerDir
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("cannot resolve workspace memory directory %q", layerDir)
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		if _, err := os.Lstat(parent); err == nil {
			realParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			return filepath.Join(append([]string{realParent}, tail...)...), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		cur = parent
	}
}

// realPathBestEffort resolves absPath as far as the filesystem allows: a
// fully existing path via EvalSymlinks; otherwise the nearest existing
// ancestor is resolved and the missing tail re-appended.
func realPathBestEffort(absPath string) (string, error) {
	if real, err := filepath.EvalSymlinks(absPath); err == nil {
		return real, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	var tail []string
	cur := absPath
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("cannot resolve %q: no existing ancestor", absPath)
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		if _, err := os.Lstat(parent); err == nil {
			realParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			return filepath.Join(append([]string{realParent}, tail...)...), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		cur = parent
	}
}

// withinDir reports whether target is dir itself or strictly beneath it.
func withinDir(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
