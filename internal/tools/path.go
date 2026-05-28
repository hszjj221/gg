package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolveInsideCWD(cwd, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is required")
	}
	root, err := filepath.Abs(cwd)
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
		return "", fmt.Errorf("path %q is outside working directory", path)
	}
	return target, nil
}
