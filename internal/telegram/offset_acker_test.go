package telegram

import (
	"sync"
	"testing"
)

// TestOffsetAckerAdvancesOnlyPastContiguousHandled verifies the at-least-once
// core: out-of-order completions must not move the offset past an update
// whose handling has not finished.
func TestOffsetAckerAdvancesOnlyPastContiguousHandled(t *testing.T) {
	var mu sync.Mutex
	var saved []int64
	acker := newOffsetAcker(5, func(next int64) {
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, next)
	})
	acker.add(5)
	acker.add(6)
	acker.add(7)

	// 6 finishes first: the frontier must wait for 5.
	acker.mark(6)
	if got := acker.frontier(); got != 5 {
		t.Fatalf("frontier = %d, want 5 (6 finished before 5)", got)
	}
	mu.Lock()
	n := len(saved)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("offset must not persist before the frontier moves, saved %v", saved)
	}

	// 5 finishes: 5 and 6 are contiguous, 7 still in flight.
	acker.mark(5)
	if got := acker.frontier(); got != 7 {
		t.Fatalf("frontier = %d, want 7", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 1 || saved[0] != 7 {
		t.Fatalf("saved offsets = %v, want [7]", saved)
	}
}

// TestOffsetAckerIgnoresStaleMarks ensures replays and duplicate marks never
// move the frontier backwards or trigger extra persists.
func TestOffsetAckerIgnoresStaleMarks(t *testing.T) {
	saves := 0
	acker := newOffsetAcker(10, func(int64) { saves++ })
	acker.mark(9) // below the frontier: already acked
	acker.mark(10)
	acker.mark(10) // duplicate
	if got := acker.frontier(); got != 10 {
		t.Fatalf("frontier = %d, want 10", got)
	}
	if saves != 0 {
		t.Fatalf("no update registered: saves = %d, want 0", saves)
	}
}

// TestOffsetAckerConcurrentMarks exercises the acker under -race: many
// workers completing in arbitrary order must converge on the right frontier
// with exactly one persist per frontier move.
func TestOffsetAckerConcurrentMarks(t *testing.T) {
	var mu sync.Mutex
	saved := map[int64]bool{}
	acker := newOffsetAcker(0, func(next int64) {
		mu.Lock()
		defer mu.Unlock()
		saved[next] = true
	})
	const n = 50
	for i := int64(0); i < n; i++ {
		acker.add(i)
	}
	var wg sync.WaitGroup
	for i := int64(0); i < n; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			acker.mark(id)
		}(i)
	}
	wg.Wait()
	if got := acker.frontier(); got != n {
		t.Fatalf("frontier = %d, want %d", got, n)
	}
	mu.Lock()
	defer mu.Unlock()
	if !saved[n] {
		t.Fatalf("final offset %d was never persisted, saved %v", n, saved)
	}
}
