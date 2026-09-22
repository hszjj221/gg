package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveInsideCWD(cwd, path string) (string, error) {
	return resolveInsideRoot(cwd, path, "working directory")
}

func resolveExistingInsideCWD(cwd, path string) (string, error) {
	return resolveExistingInsideRoot(cwd, path, "working directory")
}

func resolveWritableInsideCWD(cwd, path string) (string, error) {
	return resolveWritableInsideRoot(cwd, path, "working directory")
}

func resolveInsideRoot(root, path, rootLabel string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q is outside %s", path, rootLabel)
	}
	return target, nil
}

func resolveExistingInsideRoot(root, path, rootLabel string) (string, error) {
	target, err := resolveInsideRoot(root, path, rootLabel)
	if err != nil {
		return "", err
	}
	realRoot, canonicalTarget, err := canonicalizeRootTarget(root, target)
	if err != nil {
		return "", err
	}
	realTarget, err := resolveRealPath(canonicalTarget)
	if err != nil {
		return "", err
	}
	if err := ensureInsideRealRoot(realRoot, realTarget, path, rootLabel); err != nil {
		return "", err
	}
	return realTarget, nil
}

func resolveWritableInsideRoot(root, path, rootLabel string) (string, error) {
	target, err := resolveInsideRoot(root, path, rootLabel)
	if err != nil {
		return "", err
	}
	realRoot, canonicalTarget, err := canonicalizeRootTarget(root, target)
	if err != nil {
		return "", err
	}
	realTarget, err := resolveRealPath(canonicalTarget)
	if err == nil {
		if err := ensureInsideRealRoot(realRoot, realTarget, path, rootLabel); err != nil {
			return "", err
		}
		return realTarget, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(canonicalTarget)
	for {
		realParent, err := resolveRealPath(parent)
		if err == nil {
			if err := ensureInsideRealRoot(realRoot, realParent, path, rootLabel); err != nil {
				return "", err
			}
			return canonicalTarget, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
}

// canonicalizeRootTarget rebuilds the lexical target below the resolved root.
// This avoids mixing Windows 8.3 aliases (for example RUNNER~1) with expanded
// paths while preserving the symlink containment check below.
func canonicalizeRootTarget(root, target string) (string, string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	realRoot, err := resolveRealPath(absoluteRoot)
	if err != nil {
		return "", "", err
	}
	relativeTarget, err := filepath.Rel(absoluteRoot, target)
	if err != nil {
		return "", "", err
	}
	return realRoot, filepath.Join(realRoot, relativeTarget), nil
}

func ensureInsideRealRoot(realRoot, realTarget, originalPath, rootLabel string) error {
	rel, err := filepath.Rel(realRoot, realTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is outside %s", originalPath, rootLabel)
	}
	return nil
}
