package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// chatBinding is the persisted per-chat state: which workspace the chat
// talks in, and the agent session header ID of its current conversation.
// A chat always talks in exactly one workspace at a time; switching the
// workspace starts a fresh session in the new one.
type chatBinding struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId,omitempty"` // agent session header ID
}

// chatStoreFile is the on-disk shape of ~/.gg/channels/telegram.json.
type chatStoreFile struct {
	Version int                    `json:"version"`
	Chats   map[string]chatBinding `json:"chats"`
}

const chatStoreVersion = 1

// chatStore persists per-chat bindings. It is safe for concurrent use;
// writes are atomic (temp file + rename) with 0600 permissions.
type chatStore struct {
	mu    sync.Mutex
	path  string
	chats map[string]chatBinding
}

// loadChatStore opens (creating if needed) the chat binding file under
// homeDir (~/.gg/channels/telegram.json). A missing file yields an empty
// store; a corrupt file or an unknown layout version is an error.
func loadChatStore(homeDir string) (*chatStore, error) {
	dir := filepath.Join(homeDir, ".gg", "channels")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("telegram: create channels dir: %w", err)
	}
	s := &chatStore{path: filepath.Join(dir, "telegram.json"), chats: make(map[string]chatBinding)}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("telegram: read chat store: %w", err)
	}
	var file chatStoreFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("telegram: parse chat store %q: %w", s.path, err)
	}
	if file.Version != chatStoreVersion {
		return nil, fmt.Errorf("telegram: chat store %q has unsupported version %d", s.path, file.Version)
	}
	if file.Chats != nil {
		s.chats = file.Chats
	}
	return s, nil
}

func chatKey(chatID int64) string { return strconv.FormatInt(chatID, 10) }

// get returns the binding for a chat, or the zero binding (default
// workspace, no session) when the chat has never been seen.
func (s *chatStore) get(chatID int64) chatBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chats[chatKey(chatID)]
}

// set replaces the binding for a chat and persists the store. The swap is
// applied only after the persist succeeds, so a write failure never leaves
// the in-memory view ahead of what a restart would load.
func (s *chatStore) set(chatID int64, b chatBinding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]chatBinding, len(s.chats)+1)
	for k, v := range s.chats {
		next[k] = v
	}
	next[chatKey(chatID)] = b
	if err := saveChatStoreFile(s.path, next); err != nil {
		return err
	}
	s.chats = next
	return nil
}

func saveChatStoreFile(path string, chats map[string]chatBinding) error {
	data, err := json.MarshalIndent(chatStoreFile{Version: chatStoreVersion, Chats: chats}, "", "  ")
	if err != nil {
		return fmt.Errorf("telegram: encode chat store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "telegram-*.json")
	if err != nil {
		return fmt.Errorf("telegram: write chat store: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("telegram: write chat store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("telegram: write chat store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("telegram: write chat store: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("telegram: write chat store: %w", err)
	}
	return nil
}
