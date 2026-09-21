package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/tui"
)

func TestTreeActionBranchesBeforeSelectedUserMessage(t *testing.T) {
	executor := treeTestExecutor(t)
	items := executor.store.TreeEntries()
	selected := items[2]

	update, err := executor.handleSessionAction(tui.SessionActionTree, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := appMessageContents(executor.history); got != "question|answer" {
		t.Fatalf("history after branch = %q", got)
	}
	if update.Draft != "follow-up" || len(update.Messages) != 2 {
		t.Fatalf("unexpected tree update: %+v", update)
	}
	if err := executor.persistMessage(agent.Message{Role: agent.RoleUser, Content: "replacement"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := session.Load(executor.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := appMessageContents(loaded.Messages); got != "question|answer|replacement" {
		t.Fatalf("new active path = %q", got)
	}
	if len(loaded.Entries) != 5 {
		t.Fatalf("old branch should remain in the file, entries=%d", len(loaded.Entries))
	}
}

func TestForkActionSwitchesStoreAndPrefillsSelectedPrompt(t *testing.T) {
	executor := treeTestExecutor(t)
	originalPath := executor.store.Path()
	selected := executor.store.TreeEntries()[2]

	update, err := executor.handleSessionAction(tui.SessionActionFork, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if executor.store.Path() == originalPath || update.SessionPath != executor.store.Path() {
		t.Fatalf("fork did not switch session: old=%q new=%q update=%+v", originalPath, executor.store.Path(), update)
	}
	if update.Draft != "follow-up" || appMessageContents(executor.history) != "question|answer" {
		t.Fatalf("unexpected fork state: history=%q update=%+v", appMessageContents(executor.history), update)
	}
	loaded, err := session.Load(executor.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.ParentSession == "" {
		t.Fatalf("fork is missing parent session: %+v", loaded.Header)
	}
}

func TestCloneActionCopiesCurrentBranchToNewSession(t *testing.T) {
	executor := treeTestExecutor(t)
	originalPath := executor.store.Path()
	want := appMessageContents(executor.history)

	update, err := executor.handleSessionAction(tui.SessionActionClone, "")
	if err != nil {
		t.Fatal(err)
	}
	if executor.store.Path() == originalPath || appMessageContents(executor.history) != want || update.Draft != "" {
		t.Fatalf("unexpected clone state: path=%q history=%q update=%+v", executor.store.Path(), appMessageContents(executor.history), update)
	}
}

func treeTestExecutor(t *testing.T) *turnExecutor {
	t.Helper()
	store, err := session.NewStore(filepath.Join(t.TempDir(), "session.jsonl"), "/project")
	if err != nil {
		t.Fatal(err)
	}
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: "question"},
		{Role: agent.RoleAssistant, Content: "answer"},
		{Role: agent.RoleUser, Content: "follow-up"},
		{Role: agent.RoleAssistant, Content: "result"},
	}
	for _, message := range messages {
		if err := store.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	return newTurnExecutor(config.Config{Selection: "openai:gpt-test"}, nil, store, messages, nil, skills.Set{}, false)
}

func appMessageContents(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "|")
}
