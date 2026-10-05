package contextmgr

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
)

func BenchmarkContextBuild(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		for _, summarized := range []bool{false, true} {
			b.Run(fmt.Sprintf("records_%d/summarized_%t", count, summarized), func(b *testing.B) {
				history := make([]agent.Message, count)
				for i := range history {
					role := agent.RoleUser
					if i%2 != 0 {
						role = agent.RoleAssistant
					}
					history[i] = agent.Message{Role: role, Content: strings.Repeat("x", 256), ContentBlocks: []agent.ContentBlock{{Type: agent.ContentText, Text: "text"}}}
				}
				input := BuildInput{System: []agent.Message{{Role: agent.RoleSystem, Content: "system"}}, History: history}
				want := count + 1
				if summarized {
					input.Summary = SummaryState{Text: "summary", ThroughMessageCount: count - 20}
					want = 22
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if got := Build(input); len(got.Messages) != want {
						b.Fatal("unexpected context length")
					}
				}
			})
		}
	}
}
