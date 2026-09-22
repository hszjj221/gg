//go:build !windows

package tools

import "path/filepath"

func resolveRealPath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}
