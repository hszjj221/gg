package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFindsProjectAndGlobalSkillsWithProjectPrecedence(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "project")
	home := filepath.Join(dir, "home")
	writeSkill(t, filepath.Join(project, ".agents", "skills", "ca"), `---
name: ca
description: Project review and commit workflow.
---

# ca
`)
	writeSkill(t, filepath.Join(home, ".agents", "skills", "ca"), `---
name: ca
description: Global review and commit workflow.
---

# ca global
`)

	set, err := Load(LoadOptions{CWD: project, HomeDir: home})
	if err != nil {
		t.Fatal(err)
	}

	skill, ok := set.Find("ca")
	if !ok {
		t.Fatalf("expected ca skill")
	}
	if skill.Description != "Project review and commit workflow." {
		t.Fatalf("project skill should win, got %q", skill.Description)
	}
	if !strings.HasPrefix(skill.FilePath, filepath.Join(project, ".agents", "skills")) {
		t.Fatalf("unexpected skill location: %s", skill.FilePath)
	}
}

func TestLoadSkipsMissingDescriptionAndHiddenDirectories(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, ".agents", "skills", "missing-description"), `---
name: missing-description
---

# missing
`)
	writeSkill(t, filepath.Join(dir, ".agents", "skills", ".system", "skill-creator"), `---
name: skill-creator
description: Hidden system skill.
---

# hidden
`)

	set, err := Load(LoadOptions{CWD: dir, HomeDir: filepath.Join(dir, "home")})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := set.Find("missing-description"); ok {
		t.Fatalf("skill without description should be skipped")
	}
	if _, ok := set.Find("skill-creator"); ok {
		t.Fatalf("hidden .system skill should be skipped")
	}
	if got := set.Available(); len(got) != 0 {
		t.Fatalf("expected no available skills, got %+v", got)
	}
}

func TestLoadSupportsSymlinkedSkillDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "source", "skill")
	writeSkill(t, target, `---
name: linked
description: Linked skill.
---

# linked
`)
	link := filepath.Join(dir, ".agents", "skills", "linked")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	set, err := Load(LoadOptions{CWD: dir, HomeDir: filepath.Join(dir, "home")})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := set.Find("linked"); !ok {
		t.Fatalf("expected symlinked skill to load")
	}
}

func TestDisableModelInvocationIsNotAvailableButFindable(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, filepath.Join(dir, ".agents", "skills", "manual"), `---
name: manual
description: Manual-only skill.
disable-model-invocation: true
---

# manual
`)

	set, err := Load(LoadOptions{CWD: dir, HomeDir: filepath.Join(dir, "home")})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := set.Find("manual"); !ok {
		t.Fatalf("manual skill should be findable")
	}
	if got := set.Available(); len(got) != 0 {
		t.Fatalf("manual-only skill should not be listed as available: %+v", got)
	}
}

func TestFormatAvailableSkillsIncludesLocationAndEscapesXML(t *testing.T) {
	set := Set{skills: []Skill{{
		Name:        "review",
		Description: "Review <code> & docs",
		FilePath:    "/tmp/.agents/skills/review/SKILL.md",
		BaseDir:     "/tmp/.agents/skills/review",
	}}}

	prompt := set.FormatSystemPrompt()

	for _, want := range []string{
		"<available_skills>",
		"<name>review</name>",
		"<description>Review &lt;code&gt; &amp; docs</description>",
		"<location>/tmp/.agents/skills/review/SKILL.md</location>",
		"Use the read tool to load a skill's file",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("formatted prompt missing %q:\n%s", want, prompt)
		}
	}
}

func writeSkill(t *testing.T, dir string, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
