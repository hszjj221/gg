package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestStoreWritesAndLoadsUsageEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(agent.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("usage entry should not be loaded as a message: %+v", loaded.Messages)
	}
	if len(loaded.Usages) != 1 {
		t.Fatalf("expected one usage entry, got %d", len(loaded.Usages))
	}
	if loaded.Usages[0].Usage.TotalTokens != 10 {
		t.Fatalf("unexpected usage entry: %+v", loaded.Usages[0])
	}
}

func TestListForCWDReturnsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "project")
	first := createSession(t, dir, cwd, "first.jsonl", "first")
	second := createSession(t, dir, cwd, "second.jsonl", "second")

	infos, err := ListForCWD(dir, cwd)
	if err != nil {
		t.Fatal(err)
	}

	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(infos))
	}
	if infos[0].Path != second || infos[1].Path != first {
		t.Fatalf("sessions not sorted newest first: %+v", infos)
	}
	if infos[0].MessageCount != 1 || infos[0].Preview != "second" {
		t.Fatalf("unexpected newest session info: %+v", infos[0])
	}
}

func TestFindForCWDResolvesHeaderIDFilenameAndFilenameStem(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "project")
	path := createSession(t, dir, cwd, "target.jsonl", "hello")

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	byID, err := FindForCWD(dir, cwd, loaded.Header.ID)
	if err != nil {
		t.Fatal(err)
	}
	byFilename, err := FindForCWD(dir, cwd, "target.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	byStem, err := FindForCWD(dir, cwd, "target")
	if err != nil {
		t.Fatal(err)
	}

	if byID != path || byFilename != path || byStem != path {
		t.Fatalf("unexpected resolved paths: id=%q filename=%q stem=%q want %q", byID, byFilename, byStem, path)
	}
}

func TestLatestForCWDReturnsNewestSession(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "project")
	createSession(t, dir, cwd, "old.jsonl", "old")
	newest := createSession(t, dir, cwd, "new.jsonl", "new")

	info, err := LatestForCWD(dir, cwd)
	if err != nil {
		t.Fatal(err)
	}

	if info.Path != newest {
		t.Fatalf("unexpected latest session: %+v", info)
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

func createSession(t *testing.T, sessionDir, cwd, filename, content string) string {
	t.Helper()
	path := filepath.Join(CWDDir(sessionDir, cwd), filename)
	store, err := NewStore(path, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: content, Timestamp: time.Now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	return path
}
