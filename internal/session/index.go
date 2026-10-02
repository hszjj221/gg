package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

type Info struct {
	ID           string
	Path         string
	CWD          string
	Name         string
	Timestamp    string
	MessageCount int
	Preview      string
}

func CWDDir(sessionDir, cwd string) string {
	return filepath.Join(sessionDir, sanitizePath(canonicalCWD(cwd)))
}

// canonicalCWD maps every spelling of the same directory to one session
// key: absolute, with symlinks resolved. Callers pass raw values (".", a
// symlinked project path, ...); without this the CLI and the daemon would
// shard one project's sessions across different directories, and sessions
// written before keys were canonicalized would become unreachable after
// upgrade. Best-effort: falls back to absolute, then to the input unchanged.
func canonicalCWD(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
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
		return Info{}, fmt.Errorf("%w for %s", ErrNotFound, cwd)
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
		return "", fmt.Errorf("%w: session %q for %s", ErrNotFound, target, cwd)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("session %q is ambiguous", target)
	}
}

// FindSessionAnywhere locates a session by header ID anywhere under
// sessionDir, independent of the CWD spelling that keyed its directory. It
// is the upgrade fallback for sessions written before directory keys were
// canonicalized (keyed by spellings like "." — files sitting directly in
// sessionDir — or a symlinked path): only the first line (the header) of
// each candidate file is read, and unreadable files are skipped. New
// sessions always land in canonical directories, so this never triggers
// for them; a miss here still reports ErrNotFound.
func FindSessionAnywhere(sessionDir, targetID string) (string, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return "", fmt.Errorf("session id is required")
	}
	// Session directories are exactly one level deep.
	patterns := []string{
		filepath.Join(sessionDir, "*.jsonl"),
		filepath.Join(sessionDir, "*", "*.jsonl"),
	}
	seen := make(map[string]bool)
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, path := range matches {
			if seen[path] {
				continue
			}
			seen[path] = true
			if id, ok := peekSessionID(path); ok && id == targetID {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("%w: session %q", ErrNotFound, targetID)
}

// peekSessionID reads only the first line of a session file and reports
// the header ID. ok is false when the file cannot be read or parsed.
func peekSessionID(path string) (id string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", false
	}
	var probe struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &probe); err != nil {
		return "", false
	}
	if probe.Type != "session" || probe.ID == "" {
		return "", false
	}
	return probe.ID, true
}

func infoFromLoaded(path string, loaded Loaded) Info {
	timestamp := loaded.Header.Timestamp
	if len(loaded.records) > 0 {
		timestamp = laterTimestamp(timestamp, loaded.records[len(loaded.records)-1].timestamp())
	}
	name := ""
	if loaded.LastInfo != nil {
		name = loaded.LastInfo.Name
	}
	return Info{
		ID:           loaded.Header.ID,
		Path:         path,
		CWD:          loaded.Header.CWD,
		Name:         name,
		Timestamp:    timestamp,
		MessageCount: len(loaded.Messages),
		Preview:      preview(loaded.Messages),
	}
}

func laterTimestamp(first, second string) string {
	firstTime, firstErr := time.Parse(time.RFC3339Nano, first)
	secondTime, secondErr := time.Parse(time.RFC3339Nano, second)
	if firstErr == nil && secondErr == nil {
		if secondTime.After(firstTime) {
			return second
		}
		return first
	}
	if second > first {
		return second
	}
	return first
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
