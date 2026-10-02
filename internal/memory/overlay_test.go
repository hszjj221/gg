package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestOverlay builds an overlay with a populated global store and an
// empty workspace overlay dir (not created on disk).
func newTestOverlay(t *testing.T) (overlay *Overlay, wsDir, globalDir string) {
	t.Helper()
	tmp := t.TempDir()
	globalDir = filepath.Join(tmp, "global")
	global := NewStore(globalDir)
	if err := global.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	wsDir = filepath.Join(tmp, "ws", ".gg", "memory")
	return NewOverlay(wsDir, global), wsDir, globalDir
}

func overlayGlobal(t *testing.T, o *Overlay) *Store {
	t.Helper()
	if o.global == nil {
		t.Fatal("overlay has no global store")
	}
	return o.global
}

func TestOverlayLoadCuratedMergesWorkspaceFirst(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendCurated("global fact"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("workspace fact"); err != nil {
		t.Fatal(err)
	}

	snap, err := o.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Exists {
		t.Fatal("expected snapshot to exist")
	}
	wsIdx := strings.Index(snap.Content, "workspace fact")
	gIdx := strings.Index(snap.Content, "global fact")
	if wsIdx < 0 || gIdx < 0 {
		t.Fatalf("merged content missing entries:\n%s", snap.Content)
	}
	if wsIdx > gIdx {
		t.Fatalf("workspace entries must come first:\n%s", snap.Content)
	}
}

func TestOverlayLoadCuratedDedupsIdenticalLines(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendCurated("shared fact"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("shared fact"); err != nil {
		t.Fatal(err)
	}

	snap, err := o.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(snap.Content, "shared fact"); n != 1 {
		t.Fatalf("identical line must be kept once, got %d:\n%s", n, snap.Content)
	}
	if n := strings.Count(snap.Content, "# gg Memory"); n != 1 {
		t.Fatalf("header must be kept once, got %d:\n%s", n, snap.Content)
	}
}

func TestOverlayLoadCuratedEmptyOverlayIsPureGlobal(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendCurated("only global"); err != nil {
		t.Fatal(err)
	}

	snap, err := o.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	want, err := g.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Content != want.Content || snap.Tokens != want.Tokens || snap.Truncated != want.Truncated {
		t.Fatalf("empty overlay must degrade to pure global:\n got %+v\nwant %+v", snap, want)
	}
}

func TestOverlayLoadDailyTailMergesThenTruncates(t *testing.T) {
	o, wsDir, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	now := time.Now()
	// Bypass AppendDaily (it timestamps) to control the content exactly.
	wsDaily := filepath.Join(wsDir, now.Format("2006-01-02")+".md")
	if err := os.MkdirAll(filepath.Dir(wsDaily), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsDaily, []byte("- ws entry one\n- ws entry two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g.DailyPath(now), []byte("- global entry one\n- global entry two\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Generous budget: everything fits, deduped; workspace entries come
	// last so tail truncation preferentially keeps them (workspace
	// priority under budget pressure).
	snap, err := o.LoadDailyTail(1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ws entry one", "ws entry two", "global entry one", "global entry two"} {
		if !strings.Contains(snap.Content, want) {
			t.Fatalf("merged daily tail missing %q:\n%s", want, snap.Content)
		}
	}
	if strings.Index(snap.Content, "ws entry one") < strings.Index(snap.Content, "global entry one") {
		t.Fatalf("workspace daily entries must come last (tail keeps them):\n%s", snap.Content)
	}

	// Tiny budget: truncation applies after merging, same as loadTail.
	// Each line costs ~15-19 units; budget 10 tokens = 40 units keeps the
	// last two merged lines — which must be the workspace entries.
	small, err := o.LoadDailyTail(10)
	if err != nil {
		t.Fatal(err)
	}
	if !small.Truncated {
		t.Fatalf("expected truncation with a tiny budget, got:\n%s", small.Content)
	}
	if !strings.Contains(small.Content, "ws entry two") || strings.Contains(small.Content, "global entry one") {
		t.Fatalf("tail must keep the workspace lines under pressure:\n%s", small.Content)
	}
}

func TestOverlayPersonShadowing(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendPerson("zhang san", "global description"); err != nil {
		t.Fatal(err)
	}

	// No workspace page: falls back to global.
	snap, err := o.LoadPerson("zhang san", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Content, "global description") {
		t.Fatalf("expected global fallback, got:\n%s", snap.Content)
	}

	// Non-empty workspace page shadows the global one.
	if err := o.AppendPerson("zhang san", "workspace description"); err != nil {
		t.Fatal(err)
	}
	snap, err = o.LoadPerson("zhang san", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Content, "workspace description") || strings.Contains(snap.Content, "global description") {
		t.Fatalf("workspace page must shadow global:\n%s", snap.Content)
	}

	// An empty (whitespace-only) workspace page does not shadow.
	wsPath := o.ws.PersonPath("li si")
	if err := os.MkdirAll(filepath.Dir(wsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsPath, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := g.AppendPerson("li si", "global li si"); err != nil {
		t.Fatal(err)
	}
	snap, err = o.LoadPerson("li si", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Content, "global li si") {
		t.Fatalf("empty workspace page must fall back to global:\n%s", snap.Content)
	}

	// Group shadowing follows the same rule.
	if err := g.AppendGroup("runners", "global group"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendGroup("runners", "workspace group"); err != nil {
		t.Fatal(err)
	}
	gsnap, err := o.LoadGroup("runners", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gsnap.Content, "workspace group") {
		t.Fatalf("workspace group must shadow global:\n%s", gsnap.Content)
	}
}

func TestOverlayWritesDefaultToWorkspace(t *testing.T) {
	o, wsDir, globalDir := newTestOverlay(t)
	// The overlay dir must not exist before the first write (lazy).
	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Fatalf("overlay dir must be created lazily, stat=%v", err)
	}

	if err := o.AppendCurated("ws curated"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendDaily("ws daily"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendPerson("zhang san", "ws person"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendGroup("runners", "ws group"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(wsDir, "MEMORY.md"),
		filepath.Join(wsDir, time.Now().Format("2006-01-02")+".md"),
		filepath.Join(wsDir, "people", "zhang-san.md"),
		filepath.Join(wsDir, "groups", "runners.md"),
	} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("expected workspace-layer file %s: %v", p, err)
		}
		if strings.Contains(string(data), "global") {
			t.Fatalf("workspace file %s has unexpected content", p)
		}
	}
	if info, err := os.Stat(wsDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("overlay dir must be 0700, got %v err=%v", info, err)
	}
	// Global layer untouched by default writes.
	for _, p := range []string{
		filepath.Join(globalDir, "MEMORY.md"),
		filepath.Join(globalDir, time.Now().Format("2006-01-02")+".md"),
	} {
		if data, _ := os.ReadFile(p); strings.Contains(string(data), "ws ") {
			t.Fatalf("global file %s polluted by workspace write", p)
		}
	}

	// Explicit global writes land in the global layer.
	if err := o.AppendCuratedGlobal("global curated"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendDailyGlobal("global daily"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendPersonGlobal("li si", "global person"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendGroupGlobal("hikers", "global group"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(globalDir, "MEMORY.md"):                           "global curated",
		filepath.Join(globalDir, time.Now().Format("2006-01-02")+".md"): "global daily",
		filepath.Join(globalDir, "people", "li-si.md"):                  "global person",
		filepath.Join(globalDir, "groups", "hikers.md"):                 "global group",
	} {
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("global write missing in %s: %q err=%v", path, data, err)
		}
	}
}

func TestOverlaySearchLayeredOrderingDedupAndCap(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	// Same relative path + identical snippet in both layers: keep workspace only.
	if err := g.AppendCurated("alpha shared snippet"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("alpha shared snippet"); err != nil {
		t.Fatal(err)
	}
	// Distinct hits for ordering: equal scores -> workspace first.
	if err := g.AppendCurated("beta global only"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("beta workspace only"); err != nil {
		t.Fatal(err)
	}

	hits, err := o.SearchLayered("beta", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Layer != LayerWorkspace || !strings.Contains(hits[0].Snippet, "workspace only") {
		t.Fatalf("workspace must win score ties, got %+v", hits[0])
	}
	if hits[1].Layer != LayerGlobal {
		t.Fatalf("expected global second, got %+v", hits[1])
	}

	deduped, err := o.SearchLayered("alpha shared snippet", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(deduped) != 1 || deduped[0].Layer != LayerWorkspace {
		t.Fatalf("identical path+snippet must keep workspace hit only: %+v", deduped)
	}

	// Cap stays at 10 across merged layers. Snippets must differ per line:
	// identical path+snippet pairs dedup to one hit.
	for i := 0; i < 8; i++ {
		if err := g.AppendCurated(fmt.Sprintf("capfill global entry %d", i)); err != nil {
			t.Fatal(err)
		}
		if err := o.AppendCurated(fmt.Sprintf("capfill workspace entry %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	capped, err := o.SearchLayered("capfill", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 10 {
		t.Fatalf("expected cap of 10, got %d", len(capped))
	}
}

func TestOverlaySearchLayeredScopeAndErrors(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	if err := o.AppendCurated("scoped keyword"); err != nil {
		t.Fatal(err)
	}
	hits, err := o.SearchLayered("scoped", "curated")
	if err != nil || len(hits) != 1 {
		t.Fatalf("expected 1 curated hit, got %v err=%v", hits, err)
	}
	if _, err := o.SearchLayered("", "all"); err == nil {
		t.Fatal("expected error for empty query")
	}
	if _, err := o.SearchLayered("scoped", "bogus"); err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

func TestStoreSearchLayeredTagsGlobal(t *testing.T) {
	s := newTestStore(t)
	if err := s.AppendCurated("plain store hit"); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchLayered("plain", "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Layer != LayerGlobal {
		t.Fatalf("plain store hits must be tagged global: %+v", hits)
	}
}

func TestOverlayNoWorkspaceContextIsPureGlobal(t *testing.T) {
	tmp := t.TempDir()
	global := NewStore(filepath.Join(tmp, "global"))
	if err := global.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := global.AppendCurated("global fact"); err != nil {
		t.Fatal(err)
	}
	if err := global.AppendPerson("zhang san", "global person"); err != nil {
		t.Fatal(err)
	}

	o := NewOverlay("", global)
	if o.HasWorkspace() {
		t.Fatal("empty overlay dir must mean no workspace context")
	}

	// Reads match the bare store exactly.
	for _, tc := range []struct {
		name string
		got  func() (Snapshot, error)
		want func() (Snapshot, error)
	}{
		{"curated", func() (Snapshot, error) { return o.LoadCurated(1000) }, func() (Snapshot, error) { return global.LoadCurated(1000) }},
		{"daily", func() (Snapshot, error) { return o.LoadDailyTail(1000) }, func() (Snapshot, error) { return global.LoadDailyTail(1000) }},
		{"person", func() (Snapshot, error) { return o.LoadPerson("zhang san", 1000) }, func() (Snapshot, error) { return global.LoadPerson("zhang san", 1000) }},
	} {
		gsnap, err := tc.got()
		if err != nil {
			t.Fatal(err)
		}
		wsnap, err := tc.want()
		if err != nil {
			t.Fatal(err)
		}
		if gsnap.Content != wsnap.Content {
			t.Fatalf("%s: overlay without workspace must equal pure global:\n got %q\nwant %q", tc.name, gsnap.Content, wsnap.Content)
		}
	}

	// Writes go to the global store.
	if err := o.AppendCurated("another fact"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(global.CuratedPath())
	if err != nil || !strings.Contains(string(data), "another fact") {
		t.Fatalf("write without workspace context must land in global: %q err=%v", data, err)
	}
	if o.Dir() != global.Dir() || o.CuratedPath() != global.CuratedPath() {
		t.Fatalf("path accessors must resolve to global without workspace: dir=%q curated=%q", o.Dir(), o.CuratedPath())
	}
}

func TestOverlayCuratedSystemPromptNamesBothLayers(t *testing.T) {
	o, wsDir, globalDir := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendCurated("global fact"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("workspace fact"); err != nil {
		t.Fatal(err)
	}
	snap, err := o.LoadCurated(1000)
	if err != nil {
		t.Fatal(err)
	}
	prompt := o.CuratedSystemPrompt(snap)
	if !strings.Contains(prompt, "workspace fact") || !strings.Contains(prompt, "global fact") {
		t.Fatalf("prompt missing merged content:\n%s", prompt)
	}
	if !strings.Contains(prompt, filepath.Join(wsDir, "MEMORY.md")) ||
		!strings.Contains(prompt, filepath.Join(globalDir, "MEMORY.md")) {
		t.Fatalf("prompt must name both layer paths:\n%s", prompt)
	}
	if o.CuratedSystemPrompt(Snapshot{}) != "" {
		t.Fatal("empty snapshot must render an empty prompt")
	}
}

func TestOverlayShowMerged(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	if err := g.AppendCurated("global fact"); err != nil {
		t.Fatal(err)
	}
	if err := o.AppendCurated("workspace fact"); err != nil {
		t.Fatal(err)
	}
	shown, err := o.ShowCurated()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "workspace fact") || !strings.Contains(shown, "global fact") {
		t.Fatalf("ShowCurated must show merged content:\n%s", shown)
	}

	empty := NewOverlay(filepath.Join(t.TempDir(), "ws"), NewStore(filepath.Join(t.TempDir(), "g")))
	if shown, err := empty.ShowCurated(); err != nil || shown != "memory is empty" {
		t.Fatalf("empty overlay show = %q err=%v", shown, err)
	}
	if shown, err := empty.ShowDaily(); err != nil || shown != "memory is empty" {
		t.Fatalf("empty overlay daily show = %q err=%v", shown, err)
	}
}

func TestOverlayDirHelper(t *testing.T) {
	if got := OverlayDir("/home/u/proj"); got != filepath.Join("/home/u/proj", ".gg", "memory") {
		t.Fatalf("unexpected overlay dir: %q", got)
	}
}

func TestMergeWorkspaceGlobalPreservesWithinLayerDuplicates(t *testing.T) {
	ws := "# ws\n\n```go\ncode\n```\n\n- bullet\n"
	global := "# global\n\n```go\nother\n```\n\n- bullet\n"
	merged := mergeWorkspaceGlobal(ws, global, true)
	// The workspace layer is kept verbatim, including its repeated blank
	// lines and both fences.
	if got := strings.Count(merged, "```"); got != 2 {
		t.Fatalf("workspace fences must be preserved, got %d fences in %q", got, merged)
	}
	// The global layer's "- bullet" is shadowed by the workspace one, but
	// its fences survive (different lines).
	if !strings.Contains(merged, "other") {
		t.Fatalf("global-only content must survive, got %q", merged)
	}
	if c := strings.Count(merged, "- bullet"); c != 1 {
		t.Fatalf("cross-layer duplicate bullet must appear once, got %d in %q", c, merged)
	}
}

func TestSearchLayeredKeepsWithinLayerDuplicates(t *testing.T) {
	o, _, _ := newTestOverlay(t)
	g := overlayGlobal(t, o)
	// Twelve identical-snippet lines in one layer: all are distinct
	// matches and none may be lost to the per-layer cap or dedup.
	for i := 0; i < 12; i++ {
		if err := g.AppendCurated("repeated snippet"); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := o.SearchLayered("repeated snippet", "all")
	if err != nil {
		t.Fatal(err)
	}
	// Final cap is 10; without uncapped per-layer fetch + within-layer
	// preservation this would collapse to 1.
	if len(hits) != 10 {
		t.Fatalf("expected 10 hits (cap), got %d", len(hits))
	}
	lines := map[int]bool{}
	for _, h := range hits {
		lines[h.Line] = true
	}
	if len(lines) != 10 {
		t.Fatalf("hits must be on distinct lines, got %+v", hits)
	}
}

func TestOverlayStatusReportsMergedView(t *testing.T) {
	o, wsDir, _ := newTestOverlay(t)
	_ = wsDir
	g := overlayGlobal(t, o)
	// Global populated, workspace empty: status must reflect the merged
	// content, not exists=false/tokens=0.
	if err := g.AppendCurated("global fact"); err != nil {
		t.Fatal(err)
	}
	status, err := o.Status(1000, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "exists=true") {
		t.Fatalf("status must see global content: %q", status)
	}
	if !strings.Contains(status, "workspace=") || !strings.Contains(status, "global=") {
		t.Fatalf("status must identify both layers: %q", status)
	}
	if strings.Contains(status, "tokens=0") {
		t.Fatalf("status must count merged tokens: %q", status)
	}
}

func TestOverlayPruneWorkspaceDaily(t *testing.T) {
	o, wsDir, _ := newTestOverlay(t)
	// Plant an expired daily file directly in the workspace layer.
	old := time.Now().AddDate(0, 0, -100).Format("2006-01-02") + ".md"
	if err := os.MkdirAll(wsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, old), []byte("- old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pruned, err := o.PruneWorkspaceDaily(90)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned file, got %d", pruned)
	}
	if _, err := os.Stat(filepath.Join(wsDir, old)); !os.IsNotExist(err) {
		t.Fatal("expired daily file should be gone")
	}
}
