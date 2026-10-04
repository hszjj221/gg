package session

import (
	"encoding/json"
	"testing"
)

func TestRecordMetadataPreservesFlatJSONLFormat(t *testing.T) {
	for _, kind := range []string{"message", "usage", "model", "summary", "session_info", "head"} {
		t.Run(kind, func(t *testing.T) {
			// Existing v3 records remain readable, and shared metadata must not
			// introduce an EntryMetadata object into the flat persisted schema.
			record, err := decodeRecord([]byte(`{"type":"` + kind + `","id":"entry","parentId":"parent","timestamp":"2026-10-04T00:00:00Z"}`))
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(record.value())
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["type"]) != `"`+kind+`"` || string(fields["id"]) != `"entry"` || string(fields["parentId"]) != `"parent"` || fields["EntryMetadata"] != nil {
				t.Fatalf("schema changed: %s", data)
			}
			clone := cloneRecord(record)
			clone.setParentID(nil)
			if parent := record.parentID(); parent == nil || *parent != "parent" {
				t.Fatal("cloning shared mutable ancestry")
			}
			if clone.parentID() != nil {
				t.Fatal("cloned parent was not updated")
			}
		})
	}
}
