package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

const CurrentVersion = 2

type Header struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

type MessageEntry struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	ParentID  *string       `json:"parentId"`
	Timestamp string        `json:"timestamp"`
	Message   agent.Message `json:"message"`
}

type UsageEntry struct {
	Type      string      `json:"type"`
	ID        string      `json:"id"`
	ParentID  *string     `json:"parentId"`
	Timestamp string      `json:"timestamp"`
	Usage     agent.Usage `json:"usage"`
}

type ModelEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	Selection string  `json:"selection"`
}

type SummaryEntry struct {
	Type                string  `json:"type"`
	ID                  string  `json:"id"`
	ParentID            *string `json:"parentId"`
	Timestamp           string  `json:"timestamp"`
	Summary             string  `json:"summary"`
	ThroughMessageCount int     `json:"throughMessageCount"`
}

type SessionInfoEntry struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
	Name      string  `json:"name"`
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
	path    string
	header  Header
	records []entryRecord
	lastID  *string
}

type entryRecord struct {
	typ     string
	message *MessageEntry
	usage   *UsageEntry
	model   *ModelEntry
	summary *SummaryEntry
	info    *SessionInfoEntry
}

func (r entryRecord) id() string {
	switch r.typ {
	case "message":
		return r.message.ID
	case "usage":
		return r.usage.ID
	case "model":
		return r.model.ID
	case "summary":
		return r.summary.ID
	case "session_info":
		return r.info.ID
	default:
		return ""
	}
}

func (r entryRecord) parentID() *string {
	switch r.typ {
	case "message":
		return cloneStringPtr(r.message.ParentID)
	case "usage":
		return cloneStringPtr(r.usage.ParentID)
	case "model":
		return cloneStringPtr(r.model.ParentID)
	case "summary":
		return cloneStringPtr(r.summary.ParentID)
	case "session_info":
		return cloneStringPtr(r.info.ParentID)
	default:
		return nil
	}
}

func (r entryRecord) timestamp() string {
	switch r.typ {
	case "message":
		return r.message.Timestamp
	case "usage":
		return r.usage.Timestamp
	case "model":
		return r.model.Timestamp
	case "summary":
		return r.summary.Timestamp
	case "session_info":
		return r.info.Timestamp
	default:
		return ""
	}
}

func (r *entryRecord) setParentID(parent *string) {
	parent = cloneStringPtr(parent)
	switch r.typ {
	case "message":
		r.message.ParentID = parent
	case "usage":
		r.usage.ParentID = parent
	case "model":
		r.model.ParentID = parent
	case "summary":
		r.summary.ParentID = parent
	case "session_info":
		r.info.ParentID = parent
	}
}

func (r entryRecord) value() any {
	switch r.typ {
	case "message":
		return *r.message
	case "usage":
		return *r.usage
	case "model":
		return *r.model
	case "summary":
		return *r.summary
	case "session_info":
		return *r.info
	default:
		return nil
	}
}

func NewStore(path, cwd string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("session path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
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
		return store, nil
	}

	header := Header{Type: "session", Version: CurrentVersion, ID: newID(), Timestamp: now(), CWD: cwd}
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

func (s *Store) Header() Header {
	if s == nil {
		return Header{}
	}
	return s.header
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
	s.lastID = cloneStringPtr(id)
	return nil
}

func (s *Store) State() Loaded {
	if s == nil {
		return Loaded{}
	}
	loaded := Loaded{Header: s.header, records: cloneRecords(s.records)}
	_ = populateLoaded(&loaded, s.lastID)
	return loaded
}

func (s *Store) TreeEntries() []TreeEntry {
	if s == nil {
		return nil
	}
	active := make(map[string]bool)
	for _, record := range pathRecords(s.records, s.lastID) {
		active[record.id()] = true
	}
	recordByID := make(map[string]entryRecord, len(s.records))
	for _, record := range s.records {
		recordByID[record.id()] = record
	}

	visible := make(map[string]entryRecord)
	var order []string
	for _, record := range s.records {
		if record.typ != "message" {
			continue
		}
		visible[record.id()] = record
		order = append(order, record.id())
	}
	parents := make(map[string]*string, len(order))
	children := make(map[string][]string)
	var roots []string
	for _, id := range order {
		parent := nearestVisibleParent(recordByID, visible, visible[id].parentID())
		parents[id] = parent
		if parent == nil {
			roots = append(roots, id)
		} else {
			children[*parent] = append(children[*parent], id)
		}
	}
	result := make([]TreeEntry, 0, len(order))
	var walk func(string, int)
	walk = func(id string, depth int) {
		record := visible[id]
		result = append(result, TreeEntry{ID: id, ParentID: cloneStringPtr(parents[id]), Depth: depth, Message: record.message.Message, Active: active[id]})
		childDepth := depth
		if len(children[id]) > 1 {
			childDepth++
		}
		for _, child := range children[id] {
			walk(child, childDepth)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return result
}

// Fork creates a new session containing the path through id. Passing nil forks
// from the root. The original session and all of its branches remain untouched.
func (s *Store) Fork(id *string) (*Store, error) {
	if s == nil {
		return nil, fmt.Errorf("session persistence is disabled")
	}
	if id != nil {
		if _, ok := s.ParentID(*id); !ok {
			return nil, fmt.Errorf("session entry %q not found", *id)
		}
	}
	records := pathRecords(s.records, id)
	header := Header{Type: "session", Version: CurrentVersion, ID: newID(), Timestamp: now(), CWD: s.header.CWD, ParentSession: s.path}
	path, err := createForkFile(filepath.Dir(s.path), header, records)
	if err != nil {
		return nil, err
	}
	store := &Store{path: path, header: header, records: cloneRecords(records)}
	if len(records) > 0 {
		last := records[len(records)-1].id()
		store.lastID = &last
	}
	return store, nil
}

func (s *Store) AppendMessage(message agent.Message) error {
	if s == nil {
		return nil
	}
	if message.Timestamp == 0 {
		message.Timestamp = time.Now().UnixMilli()
	}
	entry := MessageEntry{Type: "message", ID: newID(), ParentID: cloneStringPtr(s.lastID), Timestamp: now(), Message: message}
	return s.appendRecord(entryRecord{typ: "message", message: &entry})
}

func (s *Store) AppendUsage(usage agent.Usage) error {
	if s == nil || usage.IsZero() {
		return nil
	}
	entry := UsageEntry{Type: "usage", ID: newID(), ParentID: cloneStringPtr(s.lastID), Timestamp: now(), Usage: usage}
	return s.appendRecord(entryRecord{typ: "usage", usage: &entry})
}

func (s *Store) AppendModel(provider, model string) error {
	if s == nil {
		return nil
	}
	entry := ModelEntry{Type: "model", ID: newID(), ParentID: cloneStringPtr(s.lastID), Timestamp: now(), Provider: provider, Model: model, Selection: provider + ":" + model}
	return s.appendRecord(entryRecord{typ: "model", model: &entry})
}

func (s *Store) AppendSummary(summary string, throughMessageCount int) error {
	if s == nil {
		return nil
	}
	entry := SummaryEntry{Type: "summary", ID: newID(), ParentID: cloneStringPtr(s.lastID), Timestamp: now(), Summary: summary, ThroughMessageCount: throughMessageCount}
	return s.appendRecord(entryRecord{typ: "summary", summary: &entry})
}

func (s *Store) AppendName(name string) error {
	if s == nil {
		return fmt.Errorf("session persistence is disabled")
	}
	name = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(name))
	entry := SessionInfoEntry{Type: "session_info", ID: newID(), ParentID: cloneStringPtr(s.lastID), Timestamp: now(), Name: name}
	return s.appendRecord(entryRecord{typ: "session_info", info: &entry})
}

func (s *Store) appendRecord(record entryRecord) error {
	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = writeJSONLine(file, record.value())
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	s.records = append(s.records, cloneRecord(record))
	last := record.id()
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
	reader := bufio.NewReader(file)
	lineNo := 0
	for {
		raw, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return Loaded{}, readErr
		}
		if raw == "" && readErr == io.EOF {
			break
		}
		line := strings.TrimSpace(raw)
		if lineNo > 0 && readErr == io.EOF && line != "" && !json.Valid([]byte(line)) {
			loaded.incompleteTail = true
			break
		}
		loaded.validBytes += int64(len(raw))
		loaded.needsNewline = !strings.HasSuffix(raw, "\n")
		if line == "" {
			if readErr == io.EOF {
				break
			}
			continue
		}
		lineNo++
		if lineNo == 1 {
			if err := json.Unmarshal([]byte(line), &loaded.Header); err != nil {
				return Loaded{}, err
			}
			continue
		}
		record, err := decodeRecord([]byte(line))
		if err != nil {
			return Loaded{}, err
		}
		loaded.records = append(loaded.records, record)
		if readErr == io.EOF {
			break
		}
	}
	if loaded.Header.Type != "session" {
		return Loaded{}, fmt.Errorf("missing session header")
	}
	if loaded.Header.Version > CurrentVersion {
		return Loaded{}, fmt.Errorf("unsupported session version %d", loaded.Header.Version)
	}
	if loaded.Header.Version < CurrentVersion {
		linearizeRecords(loaded.records)
		loaded.Header.Version = CurrentVersion
		loaded.migrated = true
	}
	var leaf *string
	if len(loaded.records) > 0 {
		id := loaded.records[len(loaded.records)-1].id()
		leaf = &id
	}
	if err := populateLoaded(&loaded, leaf); err != nil {
		return Loaded{}, err
	}
	return loaded, nil
}

func decodeRecord(data []byte) (entryRecord, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return entryRecord{}, err
	}
	switch probe.Type {
	case "message":
		var entry MessageEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return entryRecord{}, err
		}
		return entryRecord{typ: probe.Type, message: &entry}, nil
	case "usage":
		var entry UsageEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return entryRecord{}, err
		}
		return entryRecord{typ: probe.Type, usage: &entry}, nil
	case "model":
		var entry ModelEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return entryRecord{}, err
		}
		return entryRecord{typ: probe.Type, model: &entry}, nil
	case "summary":
		var entry SummaryEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return entryRecord{}, err
		}
		return entryRecord{typ: probe.Type, summary: &entry}, nil
	case "session_info":
		var entry SessionInfoEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return entryRecord{}, err
		}
		return entryRecord{typ: probe.Type, info: &entry}, nil
	default:
		return entryRecord{}, fmt.Errorf("unknown session entry type %q", probe.Type)
	}
}

func populateLoaded(loaded *Loaded, leaf *string) error {
	if err := validateRecords(loaded.records); err != nil {
		return err
	}
	loaded.Entries = nil
	loaded.Usages = nil
	loaded.Models = nil
	loaded.Summaries = nil
	loaded.Infos = nil
	loaded.Messages = nil
	loaded.LastModel = nil
	loaded.LastSummary = nil
	loaded.LastInfo = nil
	for _, record := range loaded.records {
		switch record.typ {
		case "message":
			loaded.Entries = append(loaded.Entries, *record.message)
		case "usage":
			loaded.Usages = append(loaded.Usages, *record.usage)
		case "model":
			loaded.Models = append(loaded.Models, *record.model)
		case "summary":
			loaded.Summaries = append(loaded.Summaries, *record.summary)
		case "session_info":
			loaded.Infos = append(loaded.Infos, *record.info)
		}
	}
	path, err := validatedPathRecords(loaded.records, leaf)
	if err != nil {
		return err
	}
	for _, record := range path {
		switch record.typ {
		case "message":
			loaded.Messages = append(loaded.Messages, record.message.Message)
		case "model":
			entry := *record.model
			loaded.LastModel = &entry
		case "summary":
			entry := *record.summary
			loaded.LastSummary = &entry
		case "session_info":
			entry := *record.info
			loaded.LastInfo = &entry
		}
	}
	return nil
}

func validateRecords(records []entryRecord) error {
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		id := record.id()
		if id == "" {
			return fmt.Errorf("session entry is missing an id")
		}
		if seen[id] {
			return fmt.Errorf("duplicate session entry id %q", id)
		}
		if parent := record.parentID(); parent != nil && !seen[*parent] {
			return fmt.Errorf("session entry %q has missing or non-append-only parent %q", id, *parent)
		}
		seen[id] = true
	}
	return nil
}

func validatedPathRecords(records []entryRecord, leaf *string) ([]entryRecord, error) {
	if leaf == nil {
		return nil, nil
	}
	byID := make(map[string]entryRecord, len(records))
	for _, record := range records {
		id := record.id()
		if id == "" {
			return nil, fmt.Errorf("session entry is missing an id")
		}
		if _, exists := byID[id]; exists {
			return nil, fmt.Errorf("duplicate session entry id %q", id)
		}
		byID[id] = record
	}
	seen := make(map[string]bool)
	current := cloneStringPtr(leaf)
	var reversed []entryRecord
	for current != nil {
		if seen[*current] {
			return nil, fmt.Errorf("session tree contains a cycle at %q", *current)
		}
		seen[*current] = true
		record, ok := byID[*current]
		if !ok {
			return nil, fmt.Errorf("session entry %q has a missing parent", *current)
		}
		reversed = append(reversed, record)
		current = record.parentID()
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed, nil
}

func pathRecords(records []entryRecord, leaf *string) []entryRecord {
	path, _ := validatedPathRecords(records, leaf)
	return cloneRecords(path)
}

func nearestVisibleParent(all map[string]entryRecord, visible map[string]entryRecord, parent *string) *string {
	seen := make(map[string]bool)
	for parent != nil && !seen[*parent] {
		seen[*parent] = true
		if _, ok := visible[*parent]; ok {
			return cloneStringPtr(parent)
		}
		record, ok := all[*parent]
		if !ok {
			return nil
		}
		parent = record.parentID()
	}
	return nil
}

func linearizeRecords(records []entryRecord) {
	var parent *string
	for i := range records {
		records[i].setParentID(parent)
		id := records[i].id()
		parent = &id
	}
}

func cloneRecords(records []entryRecord) []entryRecord {
	cloned := make([]entryRecord, len(records))
	for i, record := range records {
		cloned[i] = cloneRecord(record)
	}
	return cloned
}

func cloneRecord(record entryRecord) entryRecord {
	copy := entryRecord{typ: record.typ}
	switch record.typ {
	case "message":
		entry := *record.message
		entry.ParentID = cloneStringPtr(entry.ParentID)
		copy.message = &entry
	case "usage":
		entry := *record.usage
		entry.ParentID = cloneStringPtr(entry.ParentID)
		copy.usage = &entry
	case "model":
		entry := *record.model
		entry.ParentID = cloneStringPtr(entry.ParentID)
		copy.model = &entry
	case "summary":
		entry := *record.summary
		entry.ParentID = cloneStringPtr(entry.ParentID)
		copy.summary = &entry
	case "session_info":
		entry := *record.info
		entry.ParentID = cloneStringPtr(entry.ParentID)
		copy.info = &entry
	}
	return copy
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func createForkFile(dir string, header Header, records []entryRecord) (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		name := fmt.Sprintf("%s-fork-%s.jsonl", time.Now().UTC().Format("20060102T150405.000000000Z"), newID())
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := writeSession(file, header, records); err != nil {
			file.Close()
			_ = os.Remove(path)
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate a fork session path")
}

func rewriteSession(path string, header Header, records []entryRecord) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".session-migrate-*.jsonl")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := writeSession(file, header, records); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func writeSession(file *os.File, header Header, records []entryRecord) error {
	writer := bufio.NewWriter(file)
	if err := writeJSON(writer, header); err != nil {
		return err
	}
	for _, record := range records {
		if err := writeJSON(writer, record.value()); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return file.Sync()
}

func writeJSON(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func writeJSONLine(file *os.File, value any) error {
	if err := writeJSON(file, value); err != nil {
		return err
	}
	return file.Sync()
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
