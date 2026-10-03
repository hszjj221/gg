package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOffsetAckerHandlesGapsAndDuplicates(t *testing.T) {
	for _, start := range []int64{0, 5} {
		var saved []int64
		a := newOffsetAcker(start, func(n int64) { saved = append(saved, n) })
		a.add(100)
		a.add(103)
		a.add(200)
		a.mark(103)
		if a.frontier() != start {
			t.Fatal("advanced past unfinished update")
		}
		if a.add(103) {
			t.Fatal("duplicate dispatch allowed")
		}
		a.mark(100)
		if a.frontier() != 104 {
			t.Fatalf("frontier=%d, want104", a.frontier())
		}
		a.mark(200)
		if a.frontier() != 201 || len(saved) != 2 {
			t.Fatalf("frontier=%d saves=%v", a.frontier(), saved)
		}
		if a.add(100) {
			t.Fatal("acked replay allowed")
		}
	}
}

func TestPollingPersistsOffsetAcrossReceivedIDGaps(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]string{"username": "test"}})
			return
		}
		if r.FormValue("offset") == "104" {
			cancel()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []Update{}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []Update{{UpdateID: 100}, {UpdateID: 103}}})
	}))
	defer srv.Close()
	bot := testBot(t, nil)
	bot.api = newAPIForURL("test", srv.URL)
	if err := bot.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if bot.loadOffset() != 104 {
		t.Fatalf("saved offset=%d want104", bot.loadOffset())
	}
}
func TestPollBackoffStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitPollRetry(ctx, time.Hour) {
		t.Fatal("retry continued after cancellation")
	}
}
