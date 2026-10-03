package session

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/hszjj221/gg/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCorruptNeighborDoesNotBlockHealthySession(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	repo := NewFileRepository(root)
	store, loaded, err := repo.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(filepath.Dir(store.Path()), "broken.jsonl")
	if err = os.WriteFile(broken, []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, got, err := repo.OpenForCWD(cwd, loaded.Header.ID, false); err != nil || got.Header.ID != loaded.Header.ID {
		t.Fatalf("healthy open failed: %v", err)
	}
	infos, err := repo.List(cwd)
	if err != nil || len(infos) != 1 {
		t.Fatalf("healthy listing failed: %v %v", infos, err)
	}
}

func TestCachedValidationDetectsReplacementWithSameSizeAndTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	store, err := NewStore(path, root)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "old"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	header := store.Header()
	replacement := strings.Repeat("f", len(header.ID))
	if replacement == header.ID {
		replacement = strings.Repeat("a", len(header.ID))
	}
	data = bytes.Replace(data, []byte(header.ID), []byte(replacement), 1)
	other := filepath.Join(root, "replacement.jsonl")
	if err = os.WriteFile(other, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(other, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	if err = store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "new"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("replaced file accepted: %v", err)
	}
}
func BenchmarkSessionAppend(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("records_%d", count), func(b *testing.B) {
			root := b.TempDir()
			path := filepath.Join(root, "bench.jsonl")
			seed, err := NewStore(path, root)
			if err != nil {
				b.Fatal(err)
			}
			records := make([]entryRecord, 0, count)
			var parent *string
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("r%d", i)
				entry := MessageEntry{Type: "message", ID: id, ParentID: parent, Timestamp: now(), Message: agent.Message{Role: agent.RoleUser, Content: strings.Repeat("x", 256)}}
				records = append(records, entryRecord{typ: "message", message: &entry})
				parent = &id
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				b.Fatal(err)
			}
			if err = writeSession(f, seed.Header(), records); err != nil {
				b.Fatal(err)
			}
			f.Close()
			store, err := NewStore(path, root)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err = store.AppendMessage(agent.Message{Role: agent.RoleUser, Content: "append"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
