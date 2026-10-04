package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
)

func BenchmarkSnapshot(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("records_%d", count), func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "session.jsonl")
			var data strings.Builder
			encoder := json.NewEncoder(&data)
			if err := encoder.Encode(session.Header{Type: "session", Version: session.CurrentVersion, ID: "benchmark", CWD: dir}); err != nil {
				b.Fatal(err)
			}
			var parent *string
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("m%d", i)
				role := agent.RoleUser
				if i%2 != 0 {
					role = agent.RoleAssistant
				}
				entry := session.MessageEntry{EntryMetadata: session.EntryMetadata{Type: "message", ID: id, ParentID: parent}, Message: agent.Message{Role: role, Content: strings.Repeat("x", 256)}}
				if err := encoder.Encode(entry); err != nil {
					b.Fatal(err)
				}
				parent = &id
			}
			if err := os.WriteFile(path, []byte(data.String()), 0o600); err != nil {
				b.Fatal(err)
			}
			store, err := session.NewStore(path, dir)
			if err != nil {
				b.Fatal(err)
			}
			svc, err := NewSessionService(Options{Config: config.Config{CWD: dir}, ToolProviders: []ToolProvider{}}, store, store.State(), "")
			if err != nil {
				b.Fatal(err)
			}
			defer svc.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := svc.Snapshot(); len(got.Messages) != count || len(got.TreeItems) != count {
					b.Fatal("incomplete snapshot")
				}
			}
		})
	}
}
