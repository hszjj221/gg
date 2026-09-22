package session

import (
	"encoding/json"
	"errors"
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
	if header.Type != "session" || header.Version != CurrentVersion || header.CWD != "/tmp/project" || header.ID == "" {
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

func TestStoreRejectsStaleConcurrentWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	first, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	err = second.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "stale"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale writer error = %v, want ErrConflict", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Content != "first" {
		t.Fatalf("stale writer changed the session: %+v", loaded.Messages)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("writer lease was not released: %v", err)
	}
}

func TestStoreSerializesCompetingProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	first, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, store := range []*Store{first, second} {
		go func(index int, store *Store) {
			<-start
			results <- store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: string(rune('a' + index))})
		}(index, store)
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected writer error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestStoreRecoversStaleWriterLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	store, err := NewStore(path, "project")
	if err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, []byte("dead-writer"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleLockAge)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendName("recovered"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("stale writer lease remains: %v", err)
	}
}

func TestStoreLoadsMessagesLargerThanScannerTokenLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 128*1024)
	if err := store.AppendMessage(agent.Message{Role: agent.RoleTool, Content: large}); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != large {
		t.Fatalf("large message content was not loaded correctly")
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

func TestStoreWritesAndLoadsModelEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendModel("openai", "gpt-4.1-mini"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 1 {
		t.Fatalf("model entry should not be loaded as a message: %+v", loaded.Messages)
	}
	if len(loaded.Models) != 1 {
		t.Fatalf("expected one model entry, got %d", len(loaded.Models))
	}
	if got := loaded.Models[0].Selection; got != "openai:gpt-4.1-mini" {
		t.Fatalf("unexpected model selection: %q", got)
	}
	if loaded.LastModel == nil || loaded.LastModel.Selection != "openai:gpt-4.1-mini" {
		t.Fatalf("last model not recorded: %+v", loaded.LastModel)
	}
}

func TestStoreWritesAndLoadsSummaryEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSummary("summary one", 1); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(agent.Message{Role: agent.RoleAssistant, Content: "two"}); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSummary("summary two", 2); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("summary entries should not be loaded as messages: %+v", loaded.Messages)
	}
	if len(loaded.Summaries) != 2 {
		t.Fatalf("expected two summary entries, got %d", len(loaded.Summaries))
	}
	if loaded.LastSummary == nil || loaded.LastSummary.Summary != "summary two" || loaded.LastSummary.ThroughMessageCount != 2 {
		t.Fatalf("last summary not recorded: %+v", loaded.LastSummary)
	}
}

func TestStoreWritesAndLoadsLatestSessionName(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "session.jsonl"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendName("  First\nname  "); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendName("Final name"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Infos) != 2 {
		t.Fatalf("expected two session info entries, got %d", len(loaded.Infos))
	}
	if loaded.Infos[0].Name != "First name" {
		t.Fatalf("session name was not sanitized: %q", loaded.Infos[0].Name)
	}
	if loaded.LastInfo == nil || loaded.LastInfo.Name != "Final name" {
		t.Fatalf("latest session name not loaded: %+v", loaded.LastInfo)
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

func TestListForCWDIncludesSessionName(t *testing.T) {
	dir := t.TempDir()
	cwd := filepath.Join(dir, "project")
	path := createSession(t, dir, cwd, "named.jsonl", "hello")
	store, err := NewStore(path, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendName("Refactor auth"); err != nil {
		t.Fatal(err)
	}

	infos, err := ListForCWD(dir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Name != "Refactor auth" {
		t.Fatalf("session name missing from listing: %+v", infos)
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
