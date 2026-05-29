package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

type Info struct {
	ID           string
	Path         string
	CWD          string
	Timestamp    string
	MessageCount int
	Preview      string
}

func CWDDir(sessionDir, cwd string) string {
	return filepath.Join(sessionDir, sanitizePath(cwd))
}

func ListForCWD(sessionDir, cwd string) ([]Info, error) {
	dir := CWDDir(sessionDir, cwd)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	infos := make([]Info, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		loaded, err := Load(path)
		if err != nil {
			return nil, fmt.Errorf("load session %s: %w", path, err)
		}
		infos = append(infos, infoFromLoaded(path, loaded))
	}
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Timestamp != infos[j].Timestamp {
			return infos[i].Timestamp > infos[j].Timestamp
		}
		return infos[i].Path > infos[j].Path
	})
	return infos, nil
}

func LatestForCWD(sessionDir, cwd string) (Info, error) {
	infos, err := ListForCWD(sessionDir, cwd)
	if err != nil {
		return Info{}, err
	}
	if len(infos) == 0 {
		return Info{}, fmt.Errorf("no sessions found for %s", cwd)
	}
	return infos[0], nil
}

func FindForCWD(sessionDir, cwd, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("session id or path is required")
	}
	if looksLikePath(target) {
		path := target
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("session path is a directory: %s", path)
		}
		return path, nil
	}

	infos, err := ListForCWD(sessionDir, cwd)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, info := range infos {
		base := filepath.Base(info.Path)
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if target == info.ID || target == base || target == stem {
			matches = append(matches, info.Path)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session %q not found for %s", target, cwd)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("session %q is ambiguous", target)
	}
}

func infoFromLoaded(path string, loaded Loaded) Info {
	timestamp := loaded.Header.Timestamp
	if len(loaded.Entries) > 0 {
		timestamp = loaded.Entries[len(loaded.Entries)-1].Timestamp
	}
	return Info{
		ID:           loaded.Header.ID,
		Path:         path,
		CWD:          loaded.Header.CWD,
		Timestamp:    timestamp,
		MessageCount: len(loaded.Messages),
		Preview:      preview(loaded.Messages),
	}
}

func preview(messages []agent.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		text := strings.TrimSpace(strings.ReplaceAll(messages[i].Content, "\n", " "))
		if text != "" {
			runes := []rune(text)
			if len(runes) > 80 {
				return string(runes[:77]) + "..."
			}
			return text
		}
	}
	return ""
}

func looksLikePath(target string) bool {
	return filepath.IsAbs(target) || strings.ContainsAny(target, `/\`)
}

func sanitizePath(path string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-")
	safe := strings.Trim(replacer.Replace(path), "-")
	if safe == "" {
		return "default"
	}
	return safe
}
