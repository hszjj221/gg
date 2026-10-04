package session

import (
	"github.com/hszjj221/gg/internal/agent"
)

// EntryMetadata is shared by every JSONL record. Embedding preserves the
// existing flat wire format while keeping ancestry in one representation.
type EntryMetadata struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

func (m *EntryMetadata) metadata() *EntryMetadata { return m }

type MessageEntry struct {
	EntryMetadata
	Message agent.Message `json:"message"`
}

type UsageEntry struct {
	EntryMetadata
	Usage agent.Usage `json:"usage"`
}

type ModelEntry struct {
	EntryMetadata
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Selection string `json:"selection"`
}

type SummaryEntry struct {
	EntryMetadata
	Summary             string `json:"summary"`
	ThroughMessageCount int    `json:"throughMessageCount"`
}

type SessionInfoEntry struct {
	EntryMetadata
	Name string `json:"name"`
}

// HeadEntry persists a checkout without changing conversation content.
type HeadEntry struct{ EntryMetadata }

// recordEntry is a concrete record with shared metadata; an entryRecord has
// exactly one payload, rather than a tag and six optional pointers.
type recordEntry interface{ metadata() *EntryMetadata }

type entryRecord struct{ entry recordEntry }

func (r entryRecord) kind() string { return r.entry.metadata().Type }

func (r entryRecord) id() string { return r.entry.metadata().ID }

func (r entryRecord) parentID() *string { return cloneStringPtr(r.entry.metadata().ParentID) }

func (r entryRecord) timestamp() string { return r.entry.metadata().Timestamp }

func (r *entryRecord) setParentID(parent *string) {
	r.entry.metadata().ParentID = cloneStringPtr(parent)
}

func (r entryRecord) value() any { return r.entry }

func cloneRecords(records []entryRecord) []entryRecord {
	cloned := make([]entryRecord, len(records))
	for i, record := range records {
		cloned[i] = cloneRecord(record)
	}
	return cloned
}

func cloneRecord(record entryRecord) entryRecord {
	var cloned recordEntry
	switch entry := record.entry.(type) {
	case *MessageEntry:
		value := *entry
		cloned = &value
	case *UsageEntry:
		value := *entry
		cloned = &value
	case *ModelEntry:
		value := *entry
		cloned = &value
	case *SummaryEntry:
		value := *entry
		cloned = &value
	case *SessionInfoEntry:
		value := *entry
		cloned = &value
	case *HeadEntry:
		value := *entry
		cloned = &value
	}
	cloned.metadata().ParentID = cloneStringPtr(record.entry.metadata().ParentID)
	return entryRecord{entry: cloned}
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
