// Package userprofile loads the user's personal profile from ~/.gg/USER.md.
//
// The file is plain markdown following the project's markdown conventions.
// Lines shaped "- Key: Value" before the first "##" heading populate the
// structured fields (name, call me, timezone, language); everything else is
// preserved verbatim as notes so a round-trip never loses user content.
package userprofile

import (
	"os"
	"path/filepath"
	"strings"
)

// Profile describes the person gg is assisting.
type Profile struct {
	Name     string
	CallName string
	Timezone string
	Language string
	// Notes holds the raw markdown that did not map to a structured field:
	// every "##" section plus any unrecognized "- Key: Value" lines.
	Notes string
}

// DefaultPath returns the conventional location of the profile file.
func DefaultPath(home string) string {
	return filepath.Join(home, ".gg", "USER.md")
}

// Load reads the profile file. A missing file yields a zero Profile and no
// error so gg works before `gg init` has ever run.
func Load(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Profile{}, nil
		}
		return Profile{}, err
	}
	return parse(string(data)), nil
}

func (p Profile) Empty() bool {
	return p.Name == "" && p.CallName == "" && p.Timezone == "" &&
		p.Language == "" && strings.TrimSpace(p.Notes) == ""
}

// PromptBlock renders the profile for the agent system prompt. It returns ""
// when the profile carries nothing, so callers can skip it silently.
func (p Profile) PromptBlock() string {
	if p.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("User profile:\n")
	if p.Name != "" {
		b.WriteString("- Name: " + p.Name)
		if p.CallName != "" && p.CallName != p.Name {
			b.WriteString(" (call me " + p.CallName + ")")
		}
		b.WriteString("\n")
	} else if p.CallName != "" {
		b.WriteString("- Call me: " + p.CallName + "\n")
	}
	if p.Timezone != "" {
		b.WriteString("- Timezone: " + p.Timezone + "\n")
	}
	if p.Language != "" {
		b.WriteString("- Language: " + p.Language + "\n")
	}
	if notes := strings.TrimSpace(p.Notes); notes != "" {
		b.WriteString("\n" + notes + "\n")
	}
	return strings.TrimSpace(b.String())
}

func parse(content string) Profile {
	var p Profile
	var notes []string
	inNotes := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inNotes && strings.HasPrefix(trimmed, "##") {
			inNotes = true
		}
		if !inNotes {
			if key, value, ok := parseFieldLine(trimmed); ok {
				if value == "" {
					continue // template placeholder like "- Name:"
				}
				switch key {
				case "name":
					p.Name = value
				case "call me", "callme":
					p.CallName = value
				case "timezone", "tz":
					p.Timezone = value
				case "language", "lang":
					p.Language = value
				default:
					notes = append(notes, line) // unrecognized: preserve
				}
				continue
			}
			if trimmed == "" || trimmed == "# USER.md" {
				continue
			}
		}
		notes = append(notes, line)
	}
	p.Notes = strings.TrimSpace(strings.Join(notes, "\n"))
	return p
}

// parseFieldLine matches "- Key: Value" (value may be empty) and returns the
// lowercased key.
func parseFieldLine(line string) (key, value string, ok bool) {
	rest, found := strings.CutPrefix(line, "-")
	if !found {
		return "", "", false
	}
	k, v, found := strings.Cut(strings.TrimSpace(rest), ":")
	if !found {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(k))
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(v), true
}

// Template is the starter content written by `gg init`.
const Template = `# USER.md

- Name:
- Call me:
- Timezone:
- Language: zh

## Preferences

-

## Context

-
`

// WriteTemplate creates the profile file with the starter template. An
// existing file is left untouched so `gg init` stays idempotent.
func WriteTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(Template), 0o600)
}
