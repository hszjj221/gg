package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

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
	if loaded.Header.Version == 1 {
		linearizeRecords(loaded.records)
	}
	if loaded.Header.Version < CurrentVersion {
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
	var entry recordEntry
	switch probe.Type {
	case "message":
		entry = &MessageEntry{}
	case "usage":
		entry = &UsageEntry{}
	case "model":
		entry = &ModelEntry{}
	case "summary":
		entry = &SummaryEntry{}
	case "session_info":
		entry = &SessionInfoEntry{}
	case "head":
		entry = &HeadEntry{}
	default:
		return entryRecord{}, fmt.Errorf("unknown session entry type %q", probe.Type)
	}
	if err := json.Unmarshal(data, entry); err != nil {
		return entryRecord{}, err
	}
	return entryRecord{entry: entry}, nil
}
