package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestResumeAfterPartialJSONLAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "saved"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"message","message":`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatal("lost complete messages")
	}
	s, err = NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "resumed"}); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[1].Content != "resumed" {
		t.Fatalf("resume failed: %+v", loaded)
	}
}

func TestResumeAddsMissingFinalNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	s, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(string(data), "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "second"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatal("messages were concatenated")
	}
}

func TestLoadRejectsCorruptionBeforeLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"version\":1}\n{broken}\n{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("middle corruption was ignored")
	}
}
