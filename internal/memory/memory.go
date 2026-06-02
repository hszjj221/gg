package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const emptyMessage = "memory is empty"

type Snapshot struct {
	Path      string
	Content   string
	Tokens    int
	Truncated bool
	Exists    bool
}

func Load(path string, maxPromptTokens int) (Snapshot, error) {
	snapshot := Snapshot{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return Snapshot{}, err
	}
	snapshot.Exists = true
	content := strings.TrimSpace(string(data))
	if content == "" {
		snapshot.Tokens = 0
		return snapshot, nil
	}
	snapshot.Tokens = EstimateText(content)
	snapshot.Content, snapshot.Truncated = truncateToTokens(content, maxPromptTokens)
	return snapshot, nil
}

func Append(path, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("memory text is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	exists := true
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		exists = false
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	var b strings.Builder
	if !exists {
		b.WriteString("# gg Memory\n\n")
	} else if data, err := os.ReadFile(path); err == nil && len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "- %s\n", formatBulletText(text))
	_, err = file.WriteString(b.String())
	return err
}

func Show(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyMessage, nil
		}
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return emptyMessage, nil
	}
	return content, nil
}

func Status(path string, maxPromptTokens int, enabled bool) (string, error) {
	snapshot, err := Load(path, maxPromptTokens)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"memory: enabled=%t path=%s tokens=%d maxPromptTokens=%d exists=%t truncated=%t",
		enabled,
		path,
		snapshot.Tokens,
		maxPromptTokens,
		snapshot.Exists,
		snapshot.Truncated,
	), nil
}

func SystemPrompt(snapshot Snapshot) string {
	content := strings.TrimSpace(snapshot.Content)
	if content == "" {
		return ""
	}
	return "User memory from ~/.gg/memory.md:\n" + content
}

func EstimateText(text string) int {
	if text == "" {
		return 0
	}
	units := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 0 {
			break
		}
		if r < utf8.RuneSelf {
			units++
		} else {
			units += 4
		}
		text = text[size:]
	}
	return (units + 3) / 4
}

func formatBulletText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n  ")
}

func truncateToTokens(text string, maxPromptTokens int) (string, bool) {
	if maxPromptTokens <= 0 || EstimateText(text) <= maxPromptTokens {
		return text, false
	}
	marker := "\n\n... memory truncated ..."
	maxUnits := maxPromptTokens*4 - tokenUnits(marker)
	if maxUnits < 0 {
		maxUnits = 0
	}
	used := 0
	cut := 0
	for i, r := range text {
		next := runeUnits(r)
		if used+next > maxUnits {
			break
		}
		used += next
		cut = i + len(string(r))
	}
	return strings.TrimSpace(text[:cut]) + marker, true
}

func tokenUnits(text string) int {
	units := 0
	for _, r := range text {
		units += runeUnits(r)
	}
	return units
}

func runeUnits(r rune) int {
	if r < utf8.RuneSelf {
		return 1
	}
	return 4
}
