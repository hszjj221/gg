package userprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadParsesStructuredFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "USER.md")
	content := "# USER.md\n\n- Name: 俊杰\n- Call me: 俊杰\n- Timezone: Asia/Shanghai\n- Language: zh\n\n## Preferences\n\n- 说话直接\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "俊杰" || p.CallName != "俊杰" || p.Timezone != "Asia/Shanghai" || p.Language != "zh" {
		t.Fatalf("unexpected profile: %+v", p)
	}
	if !strings.Contains(p.Notes, "说话直接") {
		t.Fatalf("notes should keep the Preferences section: %q", p.Notes)
	}
}

func TestLoadMissingFileReturnsZeroProfile(t *testing.T) {
	p, err := Load(filepath.Join(t.TempDir(), "USER.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("expected empty profile, got %+v", p)
	}
	if p.PromptBlock() != "" {
		t.Fatalf("expected empty prompt block, got %q", p.PromptBlock())
	}
}

func TestLoadPreservesUnrecognizedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "USER.md")
	content := "# USER.md\n\n- Name: 俊杰\n- Nickname: JJ\n- Favorite-Editor: vscode\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "俊杰" {
		t.Fatalf("unexpected name: %q", p.Name)
	}
	if !strings.Contains(p.Notes, "Nickname: JJ") || !strings.Contains(p.Notes, "Favorite-Editor: vscode") {
		t.Fatalf("unrecognized keys should be preserved in notes: %q", p.Notes)
	}
}

func TestLoadSkipsEmptyTemplateFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "USER.md")
	if err := os.WriteFile(path, []byte(Template), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "" || p.CallName != "" || p.Timezone != "" {
		t.Fatalf("empty template fields should not populate the profile: %+v", p)
	}
	if p.Language != "zh" {
		t.Fatalf("expected language zh from template, got %q", p.Language)
	}
}

func TestPromptBlockRendersProfile(t *testing.T) {
	p := Profile{Name: "俊杰", CallName: "杰哥", Timezone: "Asia/Shanghai", Language: "zh", Notes: "## Preferences\n\n- 直接"}
	block := p.PromptBlock()
	for _, want := range []string{"User profile:", "Name: 俊杰 (call me 杰哥)", "Timezone: Asia/Shanghai", "Language: zh", "直接"} {
		if !strings.Contains(block, want) {
			t.Fatalf("prompt block missing %q:\n%s", want, block)
		}
	}
}

func TestWriteTemplateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gg", "USER.md")
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	custom := "# USER.md\n\n- Name: 俊杰\n"
	if err := os.WriteFile(path, []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Fatalf("WriteTemplate overwrote an existing file:\n%s", data)
	}
}
