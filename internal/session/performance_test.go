package session

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func BenchmarkTreeProjection(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("records_%d", count), func(b *testing.B) {
			records := make([]entryRecord, count)
			var parent *string
			for i := range records {
				id := fmt.Sprintf("m%d", i)
				records[i] = entryRecord{entry: &MessageEntry{EntryMetadata: EntryMetadata{Type: "message", ID: id, ParentID: parent}, Message: agent.Message{Role: agent.RoleUser, Content: strings.Repeat("x", 256)}}}
				parent = &id
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := projectTree(records, parent); len(got) != count {
					b.Fatal("incomplete tree")
				}
			}
		})
	}
}
