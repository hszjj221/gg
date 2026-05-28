package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestStoreWritesHeaderAndMessagesAsJSONL(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), "/tmp/project")
	if err != nil {
		t.Fatal(err)
	}

	msg := agent.Message{Role: agent.RoleUser, Content: "hello", Timestamp: 123}
	if err := store.AppendMessage(msg); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	lines := splitJSONLines(string(raw))
	if len(lines) != 2 {
		t.Fatalf("expected 2 jsonl lines, got %d", len(lines))
	}

	var header Header
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Type != "session" || header.Version != 1 || header.CWD != "/tmp/project" || header.ID == "" {
		t.Fatalf("unexpected header: %+v", header)
	}

	var entry MessageEntry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Type != "message" || entry.ParentID != nil || entry.Message.Content != "hello" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
}

func TestStoreLoadsMessagesAndMaintainsParentChain(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleAssistant, Content: "two"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Entries[1].ParentID == nil || *loaded.Entries[1].ParentID != loaded.Entries[0].ID {
		t.Fatalf("parent chain not maintained: %+v", loaded.Entries)
	}
}

func splitJSONLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
