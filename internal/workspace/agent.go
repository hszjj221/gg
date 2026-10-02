package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AgentDir returns the agent-private area path for a workspace root:
// <root>/.gg/agent. It is a pure path computation and never touches the
// filesystem.
//
// The agent area holds the agent's own files (drafts, downloads,
// intermediate files). Writing there via the write/edit tools skips the
// approval prompt; reading user files and every other tool keep their
// existing approval policy.
func AgentDir(root string) string {
	return filepath.Join(root, ".gg", "agent")
}

// EnsureAgentDir creates the agent area (including the <root>/.gg parent
// directory) with 0700 permissions when it does not exist yet, and returns
// its path. It is a no-op for an existing directory: permissions of an
// existing directory are never changed.
//
// Prefer EnsureRealAgentDir: it additionally refuses a symlinked agent
// area, which must never back the approval exemption.
func EnsureAgentDir(root string) (string, error) {
	dir := AgentDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// EnsureRealAgentDir creates the agent area when missing and returns its
// real (symlink-resolved) path. It fails when the agent directory itself
// is a symlink or not a directory: the approval exemption for agent-area
// writes only applies to a real directory, never to a symlinked one.
// (A symlinked agent area would let agent-private writes land elsewhere in
// the workspace without the approval those paths would otherwise need.)
func EnsureRealAgentDir(root string) (string, error) {
	dir := AgentDir(root)
	if fi, err := os.Lstat(dir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("agent area %q is a symlink", dir)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("agent area %q is not a directory", dir)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		// MkdirAll follows a dangling symlink and creates its target:
		// re-check that we ended up with a real directory.
		if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return "", fmt.Errorf("agent area %q is not a real directory", dir)
		}
	} else {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// InRealAgentDir reports whether absPath resolves inside the workspace's
// real agent area, qualifying for the approval exemption. Unlike InAgentDir
// (purely lexical), it fails closed on symlinks: a symlinked agent
// directory never grants the exemption, and the target itself is resolved
// before the containment check, so a symlinked subdirectory cannot smuggle
// a write outside the real agent area.
//
// The agent area is created (0700) when missing; the exemption then
// applies to the freshly created real directory.
func InRealAgentDir(root, absPath string) bool {
	if !InAgentDir(root, absPath) {
		return false
	}
	realDir, err := EnsureRealAgentDir(root)
	if err != nil {
		return false
	}
	realTarget, err := realPathBestEffort(absPath)
	if err != nil {
		return false
	}
	return withinDir(realDir, realTarget)
}

// realPathBestEffort resolves absPath as far as the filesystem allows: a
// fully existing path via EvalSymlinks; otherwise the nearest existing
// ancestor is resolved and the missing tail re-appended. Either way the
// result reflects where the kernel would actually land the I/O.
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

// InAgentDir reports whether absPath is inside the agent area of root. It
// is a pure path computation and never touches the filesystem.
//
// Boundary cases like <root>/.gg/agent-evil do not count: only the agent
// directory itself and paths strictly beneath it match. Letter case is
// compared per-OS: case-insensitively on Windows, case-sensitively
// everywhere else.
func InAgentDir(root, absPath string) bool {
	dir, err := filepath.Abs(AgentDir(root))
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return withinDir(strings.ToLower(dir), strings.ToLower(absPath))
	}
	return withinDir(dir, absPath)
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
