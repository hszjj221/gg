package skills

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type LoadOptions struct {
	CWD     string
	HomeDir string
}

type Skill struct {
	Name                   string
	Description            string
	FilePath               string
	BaseDir                string
	DisableModelInvocation bool
}

type Set struct {
	skills []Skill
}

func Load(options LoadOptions) (Set, error) {
	cwd := options.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Set{}, err
		}
	}
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return Set{}, err
	}
	home := options.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	roots := projectSkillRoots(cwd)
	if home != "" {
		roots = append(roots, filepath.Join(home, ".agents", "skills"))
	}

	loader := loader{byName: map[string]struct{}{}, visited: map[string]struct{}{}}
	for _, root := range roots {
		if err := loader.scan(root, true); err != nil {
			return Set{}, err
		}
	}
	return Set{skills: loader.skills}, nil
}

func (s Set) Find(name string) (Skill, bool) {
	for _, skill := range s.skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return Skill{}, false
}

func (s Set) Available() []Skill {
	out := make([]Skill, 0, len(s.skills))
	for _, skill := range s.skills {
		if !skill.DisableModelInvocation {
			out = append(out, skill)
		}
	}
	return out
}

func (s Set) ReadRoots() []string {
	roots := make([]string, 0, len(s.skills))
	seen := map[string]struct{}{}
	for _, skill := range s.skills {
		if _, ok := seen[skill.BaseDir]; ok {
			continue
		}
		seen[skill.BaseDir] = struct{}{}
		roots = append(roots, skill.BaseDir)
	}
	return roots
}

func (s Set) FormatSystemPrompt() string {
	available := s.Available()
	if len(available) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("The following skills provide specialized instructions for gg.\n")
	b.WriteString("Use the read tool to load a skill's file before applying it.\n")
	b.WriteString("When a skill file references a relative path, resolve it against the skill directory.\n")
	b.WriteString("Only use a skill when it is relevant to the user's task.\n\n")
	b.WriteString("<available_skills>\n")
	for _, skill := range available {
		b.WriteString("<skill>\n")
		writeElement(&b, "name", skill.Name)
		writeElement(&b, "description", skill.Description)
		writeElement(&b, "location", skill.FilePath)
		b.WriteString("</skill>\n")
	}
	b.WriteString("</available_skills>")
	return b.String()
}

func FormatForcedPrompt(skill Skill, content, task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		task = "Use this skill for the current request."
	}
	return fmt.Sprintf("Use the following skill instructions for this request.\n\n<skill name=%q location=%q>\nSkill directory: %s\n\n%s\n</skill>\n\nUser task:\n%s", skill.Name, skill.FilePath, skill.BaseDir, content, task)
}

func ReadSkillFile(skill Skill) (string, error) {
	data, err := os.ReadFile(skill.FilePath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type loader struct {
	skills  []Skill
	byName  map[string]struct{}
	visited map[string]struct{}
}

func (l *loader) scan(dir string, isRoot bool) error {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	if !isRoot && isHidden(filepath.Base(dir)) {
		return nil
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err == nil {
		if _, ok := l.visited[realDir]; ok {
			return nil
		}
		l.visited[realDir] = struct{}{}
	}

	skillFile := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(skillFile); err == nil {
		l.addSkill(dir, skillFile)
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		if isHidden(name) {
			continue
		}
		child := filepath.Join(dir, name)
		info, err := os.Stat(child)
		if err != nil {
			continue
		}
		if info.IsDir() {
			if err := l.scan(child, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *loader) addSkill(baseDir, filePath string) {
	skill, ok := parseSkill(baseDir, filePath)
	if !ok {
		return
	}
	if _, exists := l.byName[skill.Name]; exists {
		return
	}
	l.byName[skill.Name] = struct{}{}
	l.skills = append(l.skills, skill)
}

func parseSkill(baseDir, filePath string) (Skill, bool) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Skill{}, false
	}
	fields := parseFrontmatter(string(data))
	name := strings.TrimSpace(fields["name"])
	if name == "" {
		name = filepath.Base(baseDir)
	}
	description := strings.TrimSpace(fields["description"])
	if description == "" {
		return Skill{}, false
	}
	return Skill{
		Name:                   name,
		Description:            description,
		FilePath:               filePath,
		BaseDir:                baseDir,
		DisableModelInvocation: parseBool(fields["disable-model-invocation"]),
	}, true
}

func parseFrontmatter(content string) map[string]string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil
	}
	out := map[string]string{}
	for _, line := range lines[1:end] {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, `"'`)
		}
		out[key] = value
	}
	return out
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

func projectSkillRoots(cwd string) []string {
	var roots []string
	current := cwd
	for {
		roots = append(roots, filepath.Join(current, ".agents", "skills"))
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return roots
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

func writeElement(b *strings.Builder, name, value string) {
	b.WriteString("<")
	b.WriteString(name)
	b.WriteString(">")
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	b.Write(escaped.Bytes())
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}
