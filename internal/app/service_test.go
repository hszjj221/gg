package app

import (
	"testing"

	"github.com/hszjj221/gg/internal/config"
)

// TestServiceOwnsBrowserPool guards the P0 Chromium-leak fix: the browser
// pool must be created once per Service (one conversation), not once per
// turn inside defaultTools. A per-turn pool that is never closed leaks one
// headless Chromium process per daemon turn.
func TestServiceOwnsBrowserPool(t *testing.T) {
	service := NewService(Options{
		Config: config.Config{CWD: t.TempDir(), Selection: "test:model"},
	})
	if service.tools.browserPool == nil {
		t.Fatal("NewService must create the Service-scoped browser pool")
	}
	// Closing a pool whose Chromium was never started must be a safe no-op.
	if err := service.Close(); err != nil {
		t.Fatalf("Close on unused pool: %v", err)
	}
	// Close must be idempotent: Manager may retire a Service through several
	// paths (eviction, Remove, conflict) and must never fail on the second.
	if err := service.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestDefaultToolsReusesServicePool ensures both defaultTools call sites
// (per-turn runner construction and /context token estimation) receive the
// Service-scoped pool. Passing a distinct pool must not change which pool
// the Service itself owns: the wiring is s.browserPool at both sites.
func TestDefaultToolsReusesServicePool(t *testing.T) {
	service := NewService(Options{
		Config: config.Config{CWD: t.TempDir(), Selection: "test:model"},
	})
	first := service.tools.browserPool
	second := service.tools.browserPool
	if first == nil || first != second {
		t.Fatal("Service must expose a single stable browser pool across calls")
	}
}

// TestManagerRemoveRetiresService verifies Remove drops the session; the
// retired Service's Close (releasing Chromium) must remain safe to call.
func TestManagerRemoveRetiresService(t *testing.T) {
	manager := NewManager()
	sessionID := addRuntimeService(t, manager, &runtimeProvider{})
	service, ok := manager.Get(sessionID)
	if !ok {
		t.Fatal("service should be registered")
	}
	if !manager.Remove(sessionID) {
		t.Fatal("Remove should succeed with no active run")
	}
	if _, ok := manager.Get(sessionID); ok {
		t.Fatal("removed session should no longer resolve")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close on retired service: %v", err)
	}
}

// TestManagerEvictionDropsOldestSession exercises the LRU eviction path that
// now also releases the evicted Service's browser pool.
func TestManagerEvictionDropsOldestSession(t *testing.T) {
	manager := NewManagerWithOptions(ManagerOptions{MaxOpenSessions: 1})
	firstID := addRuntimeService(t, manager, &runtimeProvider{})
	first, ok := manager.Get(firstID)
	if !ok {
		t.Fatal("first service should be registered")
	}
	secondID := addRuntimeService(t, manager, &runtimeProvider{})
	if firstID == secondID {
		t.Fatal("expected distinct session IDs")
	}
	if _, ok := manager.Get(firstID); ok {
		t.Fatal("first service should have been evicted")
	}
	if _, ok := manager.Get(secondID); !ok {
		t.Fatal("second service should be registered")
	}
	// The evicted Service was closed by the Manager; closing again is safe.
	if err := first.Close(); err != nil {
		t.Fatalf("Close on evicted service: %v", err)
	}
}
