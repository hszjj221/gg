package app

import (
	"fmt"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/contextmgr"
	"github.com/hszjj221/gg/internal/session"
)

// conversation owns session state and its durable commits. mu protects reads
// and writes, including short file commits; it is never held across provider
// calls, tool execution or approval. beginTurn serializes both CLI and daemon.
type conversation struct {
	mu            sync.Mutex
	cfg           config.Config
	store         *session.Store
	history       []agent.Message
	summary       *session.SummaryEntry
	modelRecorded bool
	running       bool
}

func (s *conversation) HasSession() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store != nil
}

// treeItems reads the current store under mu; session.Store serializes its
// own record access, so the lock is released before walking the tree.
func (s *conversation) treeItems() []TreeItem {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	return treeItems(store)
}

func (s *conversation) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *conversation) snapshotLocked() Snapshot {
	loaded := session.Loaded{}
	path := ""
	if s.store != nil {
		loaded = s.store.State()
		path = s.store.Path()
	}
	summary := ""
	through := 0
	if s.summary != nil {
		summary = s.summary.Summary
		through = s.summary.ThroughMessageCount
	}
	return Snapshot{
		SessionID:      loaded.Header.ID,
		SessionName:    sessionName(loaded),
		SessionPath:    path,
		ModelName:      s.cfg.Selection,
		Summary:        summary,
		SummaryThrough: through,
		Messages:       append([]agent.Message{}, s.history...),
		TreeItems:      treeItems(s.store),
	}
}

func (s *conversation) RenameSession(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	return s.store.AppendName(name)
}

func (s *conversation) ensureModelRecorded() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modelRecorded {
		return nil
	}
	if err := s.appendCurrentModelLocked(); err != nil {
		return err
	}
	s.modelRecorded = true
	return nil
}

// appendCurrentModelLocked requires s.mu to be held. session.Store
// serializes its own appends, so taking its lock under s.mu is safe.
func (s *conversation) appendCurrentModelLocked() error {
	if s.store == nil {
		return nil
	}
	return s.store.AppendModel(s.cfg.Provider, s.cfg.Model)
}

func (s *conversation) buildContext(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildContextLocked(systemMessages, user)
}

// buildContextLocked requires s.mu to be held. It is CPU-only: estimation
// and message cloning, no I/O.
func (s *conversation) buildContextLocked(systemMessages []agent.Message, user agent.Message) contextmgr.BuildResult {
	return contextmgr.Build(contextmgr.BuildInput{
		System:  systemMessages,
		History: s.history,
		Current: user,
		Summary: s.summaryStateLocked(),
		Config:  s.cfg.Context,
	})
}

func (s *conversation) summaryState() contextmgr.SummaryState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryStateLocked()
}

// summaryStateLocked requires s.mu to be held.
func (s *conversation) summaryStateLocked() contextmgr.SummaryState {
	if s.summary == nil {
		return contextmgr.SummaryState{}
	}
	return contextmgr.SummaryState{Text: s.summary.Summary, ThroughMessageCount: s.summary.ThroughMessageCount}
}

func appendUsage(store *session.Store, usage agent.Usage) error {
	if store == nil {
		return nil
	}
	return store.AppendUsage(usage)
}

func cloneSummaryEntry(entry *session.SummaryEntry) *session.SummaryEntry {
	if entry == nil {
		return nil
	}
	clone := *entry
	return &clone
}

func (s *conversation) persistMessage(message agent.Message) error {
	if message.Timestamp == 0 {
		message.Timestamp = time.Now().UnixMilli()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		if err := s.store.AppendMessage(message); err != nil {
			return err
		}
	}
	s.history = append(s.history, message)
	return nil
}

// A crash can leave a durable tool call without a result. Do not replay it:
// its side effects may already have happened before the process exited.
func (s *conversation) recoverPendingTools() error {
	s.mu.Lock()
	var pending []agent.ToolCall
	seen := map[string]bool{}
	for i := len(s.history) - 1; i >= 0; i-- {
		message := s.history[i]
		if message.Role == agent.RoleTool {
			seen[message.ToolCallID] = true
			continue
		}
		pending = message.ToolCalls
		break
	}
	s.mu.Unlock()
	for _, call := range pending {
		if seen[call.ID] {
			continue
		}
		text := "Execution interrupted; result unavailable. This tool may already have run. Inspect the current state before retrying."
		if err := s.persistMessage(agent.Message{Role: agent.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Content: text, Error: text}); err != nil {
			return err
		}
	}
	return nil
}

func (s *conversation) contextBudget() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Context.MaxPromptTokens
}

func (s *conversation) maxOutputTokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Context.MaxOutputTokens
}

// recordSummary persists a summary chunk and advances the in-memory marker.
func (s *conversation) recordSummary(summary string, through int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if through < 0 || through > len(s.history) {
		return fmt.Errorf("summary position %d is outside conversation history of %d messages", through, len(s.history))
	}
	if s.store != nil {
		if err := s.store.AppendSummary(summary, through); err != nil {
			return err
		}
	}
	s.summary = &session.SummaryEntry{Summary: summary, ThroughMessageCount: through}
	return nil
}

func (s *conversation) appendUsage(usage agent.Usage) error {
	s.mu.Lock()
	store := s.store
	s.mu.Unlock()
	return appendUsage(store, usage)
}

// truncateHistory marks history[:through] as discarded without a summary so
// a failed auto-compaction degrades to hard truncation instead of failing
// the turn. The dropped messages remain in the session store; only the
// in-memory context window is truncated.
func (s *conversation) truncateHistory(through int) error {
	return s.recordSummary(truncationMarker, through)
}

func (s *conversation) applySessionState(store *session.Store, loaded session.Loaded) {
	s.store = store
	s.history = append([]agent.Message(nil), loaded.Messages...)
	s.summary = cloneSummaryEntry(loaded.LastSummary)
	s.modelRecorded = loaded.LastModel != nil && loaded.LastModel.Selection == s.cfg.Selection
}

func (s *conversation) beginTurn() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return errorf(ErrorRunConflict, true, "conversation already has an active turn")
	}
	s.running = true
	return nil
}

func (s *conversation) endTurn() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

// selectModelLocked commits the model record before exposing the new selection.
func (s *conversation) selectModelLocked(next config.Config) error {
	if s.store != nil {
		if err := s.store.AppendModel(next.Provider, next.Model); err != nil {
			return err
		}
	}
	s.cfg = next
	s.modelRecorded = true
	return nil
}
