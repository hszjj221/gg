package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func TestEveryEntryAdvancesTheTreeLeaf(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.jsonl"), "/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(agent.Usage{TotalTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendModel("openai", "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSummary("summary", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendName("named"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleAssistant, Content: "world"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.records) != 6 {
		t.Fatalf("records = %d, want 6", len(loaded.records))
	}
	for i, record := range loaded.records {
		if i == 0 {
			if record.parentID() != nil {
				t.Fatalf("first record has parent %v", record.parentID())
			}
			continue
		}
		parent := record.parentID()
		if parent == nil || *parent != loaded.records[i-1].id() {
			t.Fatalf("record %d parent = %v, want %q", i, parent, loaded.records[i-1].id())
		}
	}
	if len(loaded.Messages) != 2 || loaded.LastModel == nil || loaded.LastSummary == nil || loaded.LastInfo == nil {
		t.Fatalf("active branch was not reconstructed: %+v", loaded)
	}
}

func TestBranchPreservesOldPathAndBuildsActivePath(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.jsonl"), "/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.Message{
		{Role: agent.RoleUser, Content: "question"},
		{Role: agent.RoleAssistant, Content: "answer"},
		{Role: agent.RoleUser, Content: "old follow-up"},
		{Role: agent.RoleAssistant, Content: "old result"},
	} {
		if err := store.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	answerID := store.records[1].id()
	if err := store.Branch(&answerID); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "new follow-up"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleAssistant, Content: "new result"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Entries) != 6 {
		t.Fatalf("physical messages = %d, want 6", len(loaded.Entries))
	}
	if got := messageContents(loaded.Messages); got != "question|answer|new follow-up|new result" {
		t.Fatalf("active branch = %q", got)
	}
	tree := store.TreeEntries()
	if len(tree) != 6 {
		t.Fatalf("tree nodes = %d, want 6", len(tree))
	}
	active := map[string]bool{}
	for _, entry := range tree {
		active[entry.Message.Content] = entry.Active
	}
	if active["old follow-up"] || active["old result"] || !active["new follow-up"] || !active["new result"] {
		t.Fatalf("unexpected active path: %+v", active)
	}
	if tree[4].Depth != 1 || tree[4].Message.Content != "new follow-up" {
		t.Fatalf("alternate branch not rendered at expected depth: %+v", tree)
	}
}

func TestOpeningV1MigratesSidebandEntriesIntoLinearTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.jsonl")
	header := Header{Type: "session", Version: 1, ID: "session-1", CWD: "/project"}
	message := MessageEntry{Type: "message", ID: "message-1", Message: agent.Message{Role: agent.RoleUser, Content: "hello"}}
	parent := message.ID
	usage := UsageEntry{Type: "usage", ID: "usage-1", ParentID: &parent, Usage: agent.Usage{TotalTokens: 1}}
	assistant := MessageEntry{Type: "message", ID: "message-2", ParentID: &parent, Message: agent.Message{Role: agent.RoleAssistant, Content: "hi"}}
	data := mustJSONLine(t, header) + mustJSONLine(t, message) + mustJSONLine(t, usage) + mustJSONLine(t, assistant)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(path, "/project")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := splitJSONLines(string(raw))
	var persistedHeader Header
	var persistedAssistant MessageEntry
	if err := json.Unmarshal([]byte(lines[0]), &persistedHeader); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[3]), &persistedAssistant); err != nil {
		t.Fatal(err)
	}
	if persistedHeader.Version != CurrentVersion || persistedAssistant.ParentID == nil || *persistedAssistant.ParentID != usage.ID {
		t.Fatalf("v1 migration was not persisted: header=%+v assistant=%+v", persistedHeader, persistedAssistant)
	}
	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", loaded.Header.Version, CurrentVersion)
	}
	if loaded.Entries[1].ParentID == nil || *loaded.Entries[1].ParentID != usage.ID {
		t.Fatalf("message parent was not migrated through usage: %+v", loaded.Entries[1])
	}
	if got := messageContents(loaded.Messages); got != "hello|hi" {
		t.Fatalf("migrated messages = %q", got)
	}
}

func TestForkCopiesOnlySelectedPathAndRecordsLineage(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "session.jsonl"), "/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.Message{
		{Role: agent.RoleUser, Content: "question"},
		{Role: agent.RoleAssistant, Content: "answer"},
		{Role: agent.RoleUser, Content: "later"},
	} {
		if err := store.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	target := store.records[1].id()
	forked, err := store.Fork(&target)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(forked.Path())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.ParentSession != store.Path() || loaded.Header.ID == store.Header().ID {
		t.Fatalf("fork lineage is incorrect: %+v", loaded.Header)
	}
	if got := messageContents(loaded.Messages); got != "question|answer" {
		t.Fatalf("forked branch = %q", got)
	}
	if err := forked.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "fork only"}); err != nil {
		t.Fatal(err)
	}
	original, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := messageContents(original.Messages); got != "question|answer|later" {
		t.Fatalf("source changed after fork: %q", got)
	}
}

func messageContents(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "|")
}

func mustJSONLine(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}
