package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

type Header struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type MessageEntry struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	ParentID  *string       `json:"parentId"`
	Timestamp string        `json:"timestamp"`
	Message   agent.Message `json:"message"`
}

type Loaded struct {
	Header   Header
	Entries  []MessageEntry
	Messages []agent.Message
}

type Store struct {
	path   string
	header Header
	lastID *string
}

func NewStore(path, cwd string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("session path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		loaded, err := Load(path)
		if err != nil {
			return nil, err
		}
		store := &Store{path: path, header: loaded.Header}
		if len(loaded.Entries) > 0 {
			last := loaded.Entries[len(loaded.Entries)-1].ID
			store.lastID = &last
		}
		return store, nil
	}

	header := Header{
		Type:      "session",
		Version:   1,
		ID:        newID(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		CWD:       cwd,
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := writeJSONLine(file, header); err != nil {
		return nil, err
	}
	return &Store{path: path, header: header}, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) AppendMessage(message agent.Message) error {
	if s == nil {
		return nil
	}
	if message.Timestamp == 0 {
		message.Timestamp = time.Now().UnixMilli()
	}
	entry := MessageEntry{
		Type:      "message",
		ID:        newID(),
		ParentID:  s.lastID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Message:   message,
	}
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := writeJSONLine(file, entry); err != nil {
		return err
	}
	last := entry.ID
	s.lastID = &last
	return nil
}

func Load(path string) (Loaded, error) {
	file, err := os.Open(path)
	if err != nil {
		return Loaded{}, err
	}
	defer file.Close()

	var loaded Loaded
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineNo++
		if lineNo == 1 {
			if err := json.Unmarshal([]byte(line), &loaded.Header); err != nil {
				return Loaded{}, err
			}
			continue
		}
		var entry MessageEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return Loaded{}, err
		}
		loaded.Entries = append(loaded.Entries, entry)
		loaded.Messages = append(loaded.Messages, entry.Message)
	}
	if err := scanner.Err(); err != nil {
		return Loaded{}, err
	}
	if loaded.Header.Type != "session" {
		return Loaded{}, fmt.Errorf("missing session header")
	}
	return loaded, nil
}

func writeJSONLine(file *os.File, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
