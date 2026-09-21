package cliapp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
)

func TestTreeActionBranchesBeforeSelectedUserMessage(t *testing.T) {
	executor := treeTestExecutor(t)
	items := executor.Snapshot().TreeItems
	selected := items[2]

	update, err := executor.HandleSessionAction(app.SessionActionTree, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := appMessageContents(executor.Snapshot().Messages); got != "question|answer" {
		t.Fatalf("history after branch = %q", got)
	}
	if update.Draft != "follow-up" || len(update.Messages) != 2 {
		t.Fatalf("unexpected tree update: %+v", update)
	}
	if _, err := executor.Run(context.Background(), "replacement", nil, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := session.Load(executor.Snapshot().SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := appMessageContents(loaded.Messages); got != "question|answer|replacement|hello" {
		t.Fatalf("new active path = %q", got)
	}
	if len(loaded.Entries) != 6 {
		t.Fatalf("old branch should remain in the file, entries=%d", len(loaded.Entries))
	}
}

func TestForkActionSwitchesStoreAndPrefillsSelectedPrompt(t *testing.T) {
	executor := treeTestExecutor(t)
	originalPath := executor.Snapshot().SessionPath
	selected := executor.Snapshot().TreeItems[2]

	update, err := executor.HandleSessionAction(app.SessionActionFork, selected.ID)
	if err != nil {
		t.Fatal(err)
	}
	if executor.Snapshot().SessionPath == originalPath || update.SessionPath != executor.Snapshot().SessionPath {
		t.Fatalf("fork did not switch session: old=%q new=%q update=%+v", originalPath, executor.Snapshot().SessionPath, update)
	}
	if update.Draft != "follow-up" || appMessageContents(executor.Snapshot().Messages) != "question|answer" {
		t.Fatalf("unexpected fork state: history=%q update=%+v", appMessageContents(executor.Snapshot().Messages), update)
	}
	loaded, err := session.Load(executor.Snapshot().SessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Header.ParentSessionID == "" {
		t.Fatalf("fork is missing parent session: %+v", loaded.Header)
	}
}

func TestCloneActionCopiesCurrentBranchToNewSession(t *testing.T) {
	executor := treeTestExecutor(t)
	originalPath := executor.Snapshot().SessionPath
	want := appMessageContents(executor.Snapshot().Messages)

	update, err := executor.HandleSessionAction(app.SessionActionClone, "")
	if err != nil {
		t.Fatal(err)
	}
	if executor.Snapshot().SessionPath == originalPath || appMessageContents(executor.Snapshot().Messages) != want || update.Draft != "" {
		t.Fatalf("unexpected clone state: path=%q history=%q update=%+v", executor.Snapshot().SessionPath, appMessageContents(executor.Snapshot().Messages), update)
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
	return newTurnExecutor(config.Config{Selection: "openai:gpt-test"}, func(config.Config) agent.Provider { return &appFakeProvider{} }, store, messages, nil, skills.Set{}, false)
}

func appMessageContents(messages []agent.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "|")
}
