package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

const CurrentVersion = 3

type Header struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	// WorkspaceID is the stable ID of the workspace this session belongs to.
	// Empty for sessions written before workspace binding existed.
	WorkspaceID     string  `json:"workspaceId,omitempty"`
	ParentSessionID string  `json:"parentSessionId,omitempty"`
	ParentEntryID   *string `json:"parentEntryId,omitempty"`
	// ParentSession is retained only for reading session v2 lineage.
	ParentSession string `json:"parentSession,omitempty"`
}

type TreeEntry struct {
	ID       string
	ParentID *string
	Depth    int
	Message  agent.Message
	Active   bool
}

type Loaded struct {
	Header         Header
	Entries        []MessageEntry
	Usages         []UsageEntry
	Models         []ModelEntry
	Summaries      []SummaryEntry
	Infos          []SessionInfoEntry
	Heads          []HeadEntry
	LastModel      *ModelEntry
	LastSummary    *SummaryEntry
	LastInfo       *SessionInfoEntry
	Messages       []agent.Message
	validBytes     int64
	incompleteTail bool
	needsNewline   bool
	migrated       bool
	records        []entryRecord
}

type Store struct {
	mu       sync.Mutex
	path     string
	header   Header
	records  []entryRecord
	lastID   *string
	diskInfo os.FileInfo
}

func NewStore(path, cwd string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("session path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// MkdirAll does not tighten an existing directory: enforce owner-only
	// so upgrades from older versions (0755) are corrected.
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	release, err := acquireWriterLock(path)
	if err != nil {
		return nil, err
	}
	defer release()

	info, statErr := os.Stat(path)
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if statErr == nil && info.Size() > 0 {
		loaded, err := Load(path)
		if err != nil {
			return nil, err
		}
		if loaded.migrated {
			if err := rewriteSession(path, loaded.Header, loaded.records); err != nil {
				return nil, err
			}
		} else if loaded.incompleteTail {
			if err := os.Truncate(path, loaded.validBytes); err != nil {
				return nil, err
			}
		} else if loaded.needsNewline {
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, err
			}
			_, writeErr := file.WriteString("\n")
			if writeErr == nil {
				writeErr = file.Sync()
			}
			closeErr := file.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		store := &Store{path: path, header: loaded.Header, records: cloneRecords(loaded.records)}
		if len(store.records) > 0 {
			last := store.records[len(store.records)-1].id()
			store.lastID = &last
		}
		store.diskInfo, err = os.Stat(path)
		if err != nil {
			return nil, err
		}
		return store, nil
	}

	header := Header{Type: "session", Version: CurrentVersion, ID: newID(), Timestamp: now(), CWD: cwd}
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if statErr == nil {
		flags = os.O_WRONLY | os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := writeJSONLine(file, header); err != nil {
		return nil, err
	}
	diskInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return &Store{path: path, header: header, diskInfo: diskInfo}, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Header() Header {
	if s == nil {
		return Header{}
	}
	return s.header
}

// Identity returns the stable ID and name on the active branch without
// copying message history. Append-only parents let us walk ancestry backwards
// in a single scan, including checkouts and an explicitly cleared name.
func (s *Store) Identity() (id, name string) {
	if s == nil {
		return "", ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.lastID
	for i := len(s.records) - 1; i >= 0 && current != nil; i-- {
		record := s.records[i]
		if record.id() != *current {
			continue
		}
		if info, ok := record.entry.(*SessionInfoEntry); ok {
			return s.header.ID, info.Name
		}
		current = record.entry.metadata().ParentID
	}
	return s.header.ID, ""
}

func (s *Store) LeafID() *string {
	if s == nil {
		return nil
	}
	return cloneStringPtr(s.lastID)
}

func (s *Store) ParentID(id string) (*string, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parentIDLocked(id)
}

// parentIDLocked requires s.mu to be held.
func (s *Store) parentIDLocked(id string) (*string, bool) {
	for _, record := range s.records {
		if record.id() == id {
			return record.parentID(), true
		}
	}
	return nil, false
}

func (s *Store) Branch(id *string) error {
	if s == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	if id != nil {
		if _, ok := s.ParentID(*id); !ok {
			return fmt.Errorf("session entry %q not found", *id)
		}
	}
	entry := HeadEntry{EntryMetadata: EntryMetadata{Type: "head", ID: newID(), ParentID: cloneStringPtr(id), Timestamp: now()}}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) State() Loaded {
	if s == nil {
		return Loaded{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded := Loaded{Header: s.header, records: cloneRecords(s.records)}
	_ = populateLoaded(&loaded, s.lastID)
	return loaded
}

// Fork creates a new session containing the path through id. Passing nil forks
// from the root. The original session and all of its branches remain untouched.
func (s *Store) Fork(id *string) (*Store, error) {
	if s == nil {
		return nil, fmt.Errorf("session persistence is disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := acquireWriterLock(s.path)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := s.validateCurrentLocked(); err != nil {
		return nil, err
	}
	if id != nil {
		if _, ok := s.parentIDLocked(*id); !ok {
			return nil, fmt.Errorf("session entry %q not found", *id)
		}
	}
	records := pathRecords(s.records, id)
	header := Header{
		Type:            "session",
		Version:         CurrentVersion,
		ID:              newID(),
		Timestamp:       now(),
		CWD:             s.header.CWD,
		WorkspaceID:     s.header.WorkspaceID,
		ParentSessionID: s.header.ID,
		ParentEntryID:   cloneStringPtr(id),
	}
	path, err := createForkFile(filepath.Dir(s.path), header, records)
	if err != nil {
		return nil, err
	}
	store := &Store{path: path, header: header, records: cloneRecords(records)}
	if len(records) > 0 {
		last := records[len(records)-1].id()
		store.lastID = &last
	}
	store.diskInfo, err = os.Stat(path)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// SetWorkspaceID persists a workspace binding on the session header.
// No-op when the header already carries id. The file is rewritten
// atomically (temp file + rename, reuse rewriteSession) under the writer
// lock; use validateCurrentLocked so concurrent appends fail with
// ErrConflict instead of silently forking the file.
func (s *Store) SetWorkspaceID(id string) error {
	if s == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.header.WorkspaceID == id {
		return nil
	}
	release, err := acquireWriterLock(s.path)
	if err != nil {
		return err
	}
	defer release()
	if err := s.validateCurrentLocked(); err != nil {
		return err
	}
	header := s.header
	header.WorkspaceID = id
	if err := rewriteSession(s.path, header, s.records); err != nil {
		return err
	}
	s.header = header
	s.diskInfo, err = os.Stat(s.path)
	return err
}

func (s *Store) AppendMessage(message agent.Message) error {
	if s == nil {
		return nil
	}
	if message.Timestamp == 0 {
		message.Timestamp = time.Now().UnixMilli()
	}
	entry := MessageEntry{EntryMetadata: EntryMetadata{Type: "message", ID: newID(), Timestamp: now()}, Message: message}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) AppendUsage(usage agent.Usage) error {
	if s == nil || usage.IsZero() {
		return nil
	}
	entry := UsageEntry{EntryMetadata: EntryMetadata{Type: "usage", ID: newID(), Timestamp: now()}, Usage: usage}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) AppendModel(provider, model string) error {
	if s == nil {
		return nil
	}
	entry := ModelEntry{EntryMetadata: EntryMetadata{Type: "model", ID: newID(), Timestamp: now()}, Provider: provider, Model: model, Selection: provider + ":" + model}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) AppendSummary(summary string, throughMessageCount int) error {
	if s == nil {
		return nil
	}
	entry := SummaryEntry{EntryMetadata: EntryMetadata{Type: "summary", ID: newID(), Timestamp: now()}, Summary: summary, ThroughMessageCount: throughMessageCount}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) AppendName(name string) error {
	if s == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	name = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(name))
	entry := SessionInfoEntry{EntryMetadata: EntryMetadata{Type: "session_info", ID: newID(), Timestamp: now()}, Name: name}
	return s.appendRecord(entryRecord{entry: &entry})
}

func (s *Store) appendRecord(record entryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	release, err := acquireWriterLock(s.path)
	if err != nil {
		return err
	}
	defer release()
	if err := s.validateCurrentLocked(); err != nil {
		return err
	}
	// Parent selection happens under the store lock so a concurrent
	// rename/checkout cannot advance the head between choosing the parent
	// and appending: the record always parents onto the head as of this
	// append instead of failing with ErrConflict on a stale parent.
	if record.kind() != "head" {
		record.setParentID(cloneStringPtr(s.lastID))
	}
	if record.kind() != "head" && !sameID(record.parentID(), s.lastID) {
		return fmt.Errorf("%w: in-memory session head advanced before append", ErrConflict)
	}
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = writeJSONLine(file, record.value())
	diskInfo, statErr := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if statErr != nil {
		return statErr
	}
	s.diskInfo = diskInfo
	s.records = append(s.records, cloneRecord(record))
	last := record.id()
	s.lastID = &last
	return nil
}

func (s *Store) validateCurrentLocked() error {
	info, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	if s.diskInfo != nil && os.SameFile(s.diskInfo, info) && s.diskInfo.Size() == info.Size() && s.diskInfo.ModTime().Equal(info.ModTime()) {
		return nil
	}

	loaded, err := Load(s.path)
	if err != nil {
		return err
	}
	if loaded.incompleteTail || loaded.needsNewline || loaded.migrated {
		return fmt.Errorf("%w: session requires recovery before writing", ErrConflict)
	}
	if loaded.Header.ID != s.header.ID {
		return fmt.Errorf("%w: session identity changed", ErrConflict)
	}
	var diskLast *string
	if len(loaded.records) > 0 {
		last := loaded.records[len(loaded.records)-1].id()
		diskLast = &last
	}
	if !sameID(diskLast, s.lastID) {
		return fmt.Errorf("%w: reopen session %s", ErrConflict, s.header.ID)
	}
	return nil
}

func sameID(first, second *string) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
