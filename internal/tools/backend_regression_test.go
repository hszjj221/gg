package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentEditsAcrossToolInstancesPreserveBothChanges(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "shared.txt")
	original := "OLD_A\n" + strings.Repeat("x", 1024*1024) + "\nOLD_B\n"
	for i := 0; i < 5; i++ {
		if err = os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		results := make(chan ToolResult, 2)
		var wg sync.WaitGroup
		for _, marker := range []string{"A", "B"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				args, _ := json.Marshal(map[string]any{"path": "shared.txt", "edits": []map[string]string{{"oldText": "OLD_" + marker, "newText": "NEW_" + marker}}})
				results <- NewEditTool(root).Execute(context.Background(), args)
			}()
		}
		wg.Wait()
		close(results)
		for result := range results {
			if result.IsError {
				t.Fatalf("edit failed: %+v", result)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "NEW_A") || !strings.Contains(string(data), "NEW_B") {
			t.Fatal("successful edit was lost")
		}
	}
}
