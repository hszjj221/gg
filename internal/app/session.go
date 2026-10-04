package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/session"
)

func (s *Service) HandleSessionAction(action SessionAction, entryID string) (SessionUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return SessionUpdate{}, fmt.Errorf("cannot change conversation branch while a run is in progress")
	}

	switch action {
	case SessionActionTree:
		return s.checkoutLocked(entryID)
	case SessionActionFork:
		store, draft, notice, err := s.forkStoreLocked(entryID)
		if err != nil {
			return SessionUpdate{}, err
		}
		s.applySessionState(store, store.State())
		return s.sessionUpdateLocked(draft, notice), nil
	case SessionActionClone:
		store, notice, err := s.cloneStoreLocked()
		if err != nil {
			return SessionUpdate{}, err
		}
		s.applySessionState(store, store.State())
		return s.sessionUpdateLocked("", notice), nil
	default:
		return SessionUpdate{}, fmt.Errorf("unknown session action %q", action)
	}
}

func (s *Service) Checkout(entryID string) (SessionUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return SessionUpdate{}, fmt.Errorf("cannot change conversation branch while a run is in progress")
	}
	return s.checkoutLocked(entryID)
}

func (s *Service) checkoutLocked(entryID string) (SessionUpdate, error) {
	if s.store == nil {
		return SessionUpdate{}, fmt.Errorf("session persistence is disabled")
	}
	entry, ok := findTreeEntry(s.store.TreeEntries(), entryID)
	if !ok {
		return SessionUpdate{}, fmt.Errorf("conversation node %q not found", entryID)
	}
	target := &entry.ID
	draft := ""
	if entry.Message.Role == agent.RoleUser {
		var found bool
		target, found = s.store.ParentID(entry.ID)
		if !found {
			return SessionUpdate{}, fmt.Errorf("conversation node %q not found", entryID)
		}
		draft = agent.MessageText(entry.Message)
	}
	if err := s.store.Branch(target); err != nil {
		return SessionUpdate{}, err
	}
	loaded := s.store.State()
	s.applySessionState(s.store, loaded)
	return s.sessionUpdateLocked(draft, "switched conversation branch"), nil
}

// Fork creates an independent conversation service while leaving the source
// service on its current session. Multi-client transports should use this
// method; HandleSessionAction retains the TUI's switch-after-fork behavior.
func (s *Service) Fork(entryID string) (*Service, SessionUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forkLocked(entryID)
}

func (s *Service) forkLocked(entryID string) (*Service, SessionUpdate, error) {
	store, draft, notice, err := s.forkStoreLocked(entryID)
	if err != nil {
		return nil, SessionUpdate{}, err
	}
	child, err := s.childServiceLocked(store)
	if err != nil {
		return nil, SessionUpdate{}, err
	}
	return child, child.sessionUpdateUnlocked(draft, notice), nil
}

func (s *Service) forkStoreLocked(entryID string) (*session.Store, string, string, error) {
	if s.store == nil {
		return nil, "", "", fmt.Errorf("session persistence is disabled")
	}
	entry, ok := findTreeEntry(s.store.TreeEntries(), entryID)
	if !ok || entry.Message.Role != agent.RoleUser {
		return nil, "", "", fmt.Errorf("select a user message to fork")
	}
	parent, found := s.store.ParentID(entry.ID)
	if !found {
		return nil, "", "", fmt.Errorf("conversation node %q not found", entryID)
	}
	store, err := s.store.Fork(parent)
	if err != nil {
		return nil, "", "", err
	}
	return store, agent.MessageText(entry.Message), "forked to " + filepath.Base(store.Path()), nil
}

func (s *Service) Clone() (*Service, SessionUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cloneLocked()
}

func (s *Service) cloneLocked() (*Service, SessionUpdate, error) {
	store, notice, err := s.cloneStoreLocked()
	if err != nil {
		return nil, SessionUpdate{}, err
	}
	child, err := s.childServiceLocked(store)
	if err != nil {
		return nil, SessionUpdate{}, err
	}
	return child, child.sessionUpdateUnlocked("", notice), nil
}

func (s *Service) cloneStoreLocked() (*session.Store, string, error) {
	if s.store == nil {
		return nil, "", fmt.Errorf("session persistence is disabled")
	}
	store, err := s.store.Fork(s.store.LeafID())
	if err != nil {
		return nil, "", err
	}
	return store, "cloned to " + filepath.Base(store.Path()), nil
}

func (s *Service) childServiceLocked(store *session.Store) (*Service, error) {
	return NewSessionService(Options{
		Config: s.cfg, ProviderFactory: s.providerFactory,
		Skills: s.skillSet, Profile: s.profile, MemoryStore: s.memStore,
		Log: s.tools.logger, Degraded: s.tools.degraded, ToolProviders: s.tools.providers,
	}, store, store.State(), s.cfg.Selection)
}

func (s *Service) sessionUpdateLocked(draft, notice string) SessionUpdate {
	return s.sessionUpdateUnlocked(draft, notice)
}

// sessionUpdateUnlocked is only used on a newly constructed child that is not
// yet shared with another goroutine.
func (s *Service) sessionUpdateUnlocked(draft, notice string) SessionUpdate {
	loaded := s.store.State()
	return SessionUpdate{
		Messages:    append([]agent.Message{}, s.history...),
		TreeItems:   treeItems(s.store),
		SessionID:   loaded.Header.ID,
		SessionName: sessionName(loaded),
		ModelName:   s.cfg.Selection,
		SessionPath: s.store.Path(),
		Draft:       draft,
		Notice:      notice,
	}
}

func treeItems(store *session.Store) []TreeItem {
	if store == nil {
		return []TreeItem{}
	}
	entries := store.TreeEntries()
	items := make([]TreeItem, 0, len(entries))
	for _, entry := range entries {
		text := agent.MessageText(entry.Message)
		if text == "" && len(entry.Message.ToolCalls) > 0 {
			names := make([]string, 0, len(entry.Message.ToolCalls))
			for _, call := range entry.Message.ToolCalls {
				names = append(names, call.Name)
			}
			text = "tool call: " + strings.Join(names, ", ")
		}
		items = append(items, TreeItem{
			ID:       entry.ID,
			ParentID: cloneStringPtr(entry.ParentID),
			Depth:    entry.Depth,
			Role:     entry.Message.Role,
			Text:     text,
			Active:   entry.Active,
		})
	}
	return items
}

func findTreeEntry(entries []session.TreeEntry, id string) (session.TreeEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return session.TreeEntry{}, false
}

func sessionName(loaded session.Loaded) string {
	if loaded.LastInfo == nil {
		return ""
	}
	return loaded.LastInfo.Name
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
