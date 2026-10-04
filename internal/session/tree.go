package session

import (
	"fmt"
)

func (s *Store) TreeEntries() []TreeEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return projectTree(s.records, s.lastID)
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
	loaded.Heads = nil
	loaded.Messages = nil
	loaded.LastModel = nil
	loaded.LastSummary = nil
	loaded.LastInfo = nil
	for _, record := range loaded.records {
		switch record.kind() {
		case "message":
			loaded.Entries = append(loaded.Entries, *record.entry.(*MessageEntry))
		case "usage":
			loaded.Usages = append(loaded.Usages, *record.entry.(*UsageEntry))
		case "model":
			loaded.Models = append(loaded.Models, *record.entry.(*ModelEntry))
		case "summary":
			loaded.Summaries = append(loaded.Summaries, *record.entry.(*SummaryEntry))
		case "session_info":
			loaded.Infos = append(loaded.Infos, *record.entry.(*SessionInfoEntry))
		case "head":
			loaded.Heads = append(loaded.Heads, *record.entry.(*HeadEntry))
		}
	}
	path, err := validatedPathRecords(loaded.records, leaf)
	if err != nil {
		return err
	}
	for _, record := range path {
		switch record.kind() {
		case "message":
			loaded.Messages = append(loaded.Messages, record.entry.(*MessageEntry).Message)
		case "model":
			entry := *record.entry.(*ModelEntry)
			loaded.LastModel = &entry
		case "summary":
			entry := *record.entry.(*SummaryEntry)
			loaded.LastSummary = &entry
		case "session_info":
			entry := *record.entry.(*SessionInfoEntry)
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

// projectTree projects visible branches without filesystem access or locks.
func projectTree(records []entryRecord, leaf *string) []TreeEntry {
	recordByID := make(map[string]entryRecord, len(records))
	for _, record := range records {
		recordByID[record.id()] = record
	}
	active := make(map[string]bool)
	// Records were validated when loaded and parents only point backwards.
	// Projection reads their metadata; it does not need a cloned ancestry path.
	for current := leaf; current != nil; {
		record := recordByID[*current]
		active[*current] = true
		current = record.entry.metadata().ParentID
	}

	visible := make(map[string]entryRecord)
	var order []string
	for _, record := range records {
		if record.kind() != "message" {
			continue
		}
		visible[record.id()] = record
		order = append(order, record.id())
	}
	parents := make(map[string]*string, len(order))
	children := make(map[string][]string)
	var roots []string
	for _, id := range order {
		parent := nearestVisibleParent(recordByID, visible, visible[id].entry.metadata().ParentID)
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
		result = append(result, TreeEntry{ID: id, ParentID: parents[id], Depth: depth, Message: record.entry.(*MessageEntry).Message, Active: active[id]})
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
