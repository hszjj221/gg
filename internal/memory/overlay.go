package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Layer names tagging where a memory entry or search hit came from.
const (
	LayerWorkspace = "workspace"
	LayerGlobal    = "global"
)

// SearchHit is a keyword match tagged with its layer of origin.
type SearchHit struct {
	Hit
	Layer string // LayerWorkspace or LayerGlobal
}

// ToolStore is the memory surface the agent tools (memory_add,
// memory_search) need: appends with an explicit global-layer path, plus
// layered search. Both *Store and *Overlay satisfy it.
type ToolStore interface {
	AppendCurated(text string) error
	AppendDaily(text string) error
	AppendPerson(slug, text string) error
	AppendGroup(slug, text string) error
	// AppendCuratedGlobal and friends write to the global layer even when
	// the default write target is the workspace layer (memory_add
	// target=global). On a plain *Store they are identical to the
	// non-Global variants.
	AppendCuratedGlobal(text string) error
	AppendDailyGlobal(text string) error
	AppendPersonGlobal(slug, text string) error
	AppendGroupGlobal(slug, text string) error
	// SearchLayered searches every layer; hits carry their layer of
	// origin. See Overlay.SearchLayered for ordering and dedup rules.
	SearchLayered(query, scope string) ([]SearchHit, error)
}

// StoreAPI is the full memory surface the conversation service needs:
// ToolStore plus path accessors, layered reads, and prompt rendering.
type StoreAPI interface {
	ToolStore
	// Dir, CuratedPath, DailyPath, PersonPath and GroupPath name the
	// primary (default write target) layer: the workspace overlay when a
	// workspace context exists, otherwise the global layer.
	Dir() string
	CuratedPath() string
	DailyPath(time.Time) string
	PersonPath(slug string) string
	GroupPath(slug string) string
	LoadCurated(maxPromptTokens int) (Snapshot, error)
	LoadDailyTail(maxPromptTokens int) (Snapshot, error)
	// LoadPerson/LoadGroup read one page with shadowing: a non-empty
	// workspace page wins, otherwise the global page is used.
	LoadPerson(slug string, maxPromptTokens int) (Snapshot, error)
	LoadGroup(slug string, maxPromptTokens int) (Snapshot, error)
	// ShowCurated/ShowDaily return the merged human-readable content
	// (what the prompt sees), for /memory show.
	ShowCurated() (string, error)
	ShowDaily() (string, error)
	// CuratedSystemPrompt renders the "User memory from ..." prompt block
	// for a snapshot produced by LoadCurated, naming both layer paths on
	// an overlay.
	CuratedSystemPrompt(snapshot Snapshot) string
	// Status reports the merged memory status (what the prompt sees),
	// naming the layer paths.
	Status(maxPromptTokens int, enabled bool) (string, error)
}

// OverlayDir returns the conventional workspace memory overlay directory:
// <wsRoot>/.gg/memory.
func OverlayDir(wsRoot string) string {
	return filepath.Join(wsRoot, ".gg", "memory")
}

// Overlay is a two-layer view over memory: a workspace overlay
// (<root>/.gg/memory/) in front of the global store (~/.gg/memory/).
//
// Read semantics (per the workspace design, Appendix A):
//   - LoadCurated: merge, workspace entries first, global after,
//     byte-identical lines deduped (kept at the workspace position); the
//     token-budget truncation drops the tail, so workspace entries survive.
//   - LoadDailyTail: merge, global first and workspace LAST, because the
//     tail truncation keeps the last lines — this way budget pressure drops
//     global lines first and the workspace lines survive (workspace
//     priority); byte-identical lines deduped.
//   - LoadPerson / LoadGroup: shadow — a non-empty workspace page wins,
//     otherwise the global page is used.
//   - SearchLayered: both layers are searched uncapped, then merged;
//     a global hit whose relative path and snippet match a workspace hit
//     is dropped (workspace shadows global); hits within one layer are
//     never collapsed. On equal scores workspace hits sort first; the
//     result cap stays at 10.
//
// Write semantics: Append* writes to the workspace layer by default (its
// directory is created lazily, 0700; reads never create directories).
// Append*Global writes to the global layer explicitly. With no workspace
// context (overlayDir == ""), the overlay degrades to pure-global
// behavior: every method behaves like the global store alone.
type Overlay struct {
	ws     *Store // nil means no workspace context
	global *Store
}

// NewOverlay builds the two-layer view. overlayDir is the workspace layer
// root (see OverlayDir); empty means no workspace context. global must be
// non-nil. The workspace layer is strict: symlinked memory files or
// directories are rejected instead of followed (see symlink.go).
func NewOverlay(overlayDir string, global *Store) *Overlay {
	o := &Overlay{global: global}
	if strings.TrimSpace(overlayDir) != "" {
		o.ws = NewWorkspaceStore(overlayDir)
	}
	return o
}

// HasWorkspace reports whether a workspace layer is present.
func (o *Overlay) HasWorkspace() bool { return o.ws != nil }

// WorkspaceDir returns the workspace layer root, or "" when there is no
// workspace context.
func (o *Overlay) WorkspaceDir() string {
	if o.ws == nil {
		return ""
	}
	return o.ws.Dir()
}

// GlobalDir returns the global layer root.
func (o *Overlay) GlobalDir() string { return o.global.Dir() }

// primary is the store default writes land in.
func (o *Overlay) primary() *Store {
	if o.ws != nil {
		return o.ws
	}
	return o.global
}

func (o *Overlay) Dir() string                   { return o.primary().Dir() }
func (o *Overlay) CuratedPath() string           { return o.primary().CuratedPath() }
func (o *Overlay) DailyPath(t time.Time) string  { return o.primary().DailyPath(t) }
func (o *Overlay) PersonPath(slug string) string { return o.primary().PersonPath(slug) }
func (o *Overlay) GroupPath(slug string) string  { return o.primary().GroupPath(slug) }

// readLayerFile reads one layer's file. Missing files are not an error;
// the content is trimmed so emptiness checks are whitespace-insensitive.
func readLayerFile(path string) (content string, exists bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(string(raw)), true, nil
}

// mergeWorkspaceGlobal merges the workspace and global layer contents.
// wsFirst selects the output order: true puts workspace lines first
// (curated content, where head truncation drops the tail); false puts
// global lines first (daily tail, where truncation drops the head so
// workspace lines survive).
//
// Cross-layer duplicates are always resolved in favor of the workspace
// layer, regardless of output order: a byte-identical line already
// present in the workspace layer is dropped from the global layer.
// Duplicates within a single layer are preserved — collapsing them would
// corrupt documents with repeated blank lines, bullets, or Markdown
// fences.
func mergeWorkspaceGlobal(ws, global string, wsFirst bool) string {
	if ws == "" {
		return global
	}
	if global == "" {
		return ws
	}
	wsLines := make(map[string]struct{})
	for _, line := range strings.Split(ws, "\n") {
		wsLines[line] = struct{}{}
	}
	// filterGlobal drops global lines shadowed by the workspace layer.
	filterGlobal := func() []string {
		var out []string
		for _, line := range strings.Split(global, "\n") {
			if _, ok := wsLines[line]; ok {
				continue
			}
			out = append(out, line)
		}
		return out
	}
	wsSplit := strings.Split(ws, "\n")
	if wsFirst {
		return strings.Join(append(wsSplit, filterGlobal()...), "\n")
	}
	return strings.Join(append(filterGlobal(), wsSplit...), "\n")
}

// layerContents returns the non-empty contents of the workspace layer
// (when present) and the global layer, for path (a per-layer path
// function like CuratedPath).
func (o *Overlay) layerContents(path func(*Store) string) (ws, global string, exists bool, err error) {
	if o.ws != nil {
		p, err := o.ws.resolve(path(o.ws))
		if err != nil {
			return "", "", false, err
		}
		content, fileExists, err := readLayerFile(p)
		if err != nil {
			return "", "", false, err
		}
		if fileExists {
			exists = true
		}
		ws = content
	}
	content, fileExists, err := readLayerFile(path(o.global))
	if err != nil {
		return "", "", false, err
	}
	if fileExists {
		exists = true
	}
	global = content
	return ws, global, exists, nil
}

func (o *Overlay) LoadCurated(maxPromptTokens int) (Snapshot, error) {
	snapshot := Snapshot{Path: o.CuratedPath()}
	ws, global, exists, err := o.layerContents(func(s *Store) string { return s.CuratedPath() })
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Exists = exists
	merged := mergeWorkspaceGlobal(ws, global, true)
	if merged == "" {
		return snapshot, nil
	}
	snapshot.Tokens = EstimateText(merged)
	snapshot.Content, snapshot.Truncated = truncateToTokens(merged, maxPromptTokens)
	return snapshot, nil
}

// cutTailContent keeps the most recent lines of content within the token
// budget. It mirrors loadTail's algorithm, applied to already-merged
// content (loadTail itself lives in store.go and reads one file).
func cutTailContent(content string, maxPromptTokens int) (kept string, truncated bool) {
	lines := strings.Split(content, "\n")
	maxUnits := maxPromptTokens * 4
	used := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		next := tokenUnits(lines[i]) + 1 // +1 for the newline
		if used+next > maxUnits {
			break
		}
		used += next
		start = i
	}
	return strings.Join(lines[start:], "\n"), start > 0
}

func (o *Overlay) LoadDailyTail(maxPromptTokens int) (Snapshot, error) {
	now := time.Now()
	snapshot := Snapshot{Path: o.DailyPath(now)}
	ws, global, exists, err := o.layerContents(func(s *Store) string { return s.DailyPath(now) })
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Exists = exists
	// Tail truncation keeps the LAST lines, so the workspace layer goes
	// last: under budget pressure the global lines are dropped first and
	// the workspace lines (the more relevant ones in a project workspace)
	// survive. Cross-layer duplicates still resolve workspace-first.
	merged := mergeWorkspaceGlobal(ws, global, false)
	if merged == "" || maxPromptTokens <= 0 {
		return snapshot, nil
	}
	snapshot.Content, snapshot.Truncated = cutTailContent(merged, maxPromptTokens)
	snapshot.Tokens = EstimateText(merged)
	return snapshot, nil
}

func (o *Overlay) LoadPerson(slug string, maxPromptTokens int) (Snapshot, error) {
	if o.ws != nil {
		p, err := o.ws.resolve(o.ws.PersonPath(slug))
		if err != nil {
			return Snapshot{}, err
		}
		content, _, err := readLayerFile(p)
		if err != nil {
			return Snapshot{}, err
		}
		if content != "" {
			return Load(p, maxPromptTokens)
		}
	}
	return o.global.LoadPerson(slug, maxPromptTokens)
}

func (o *Overlay) LoadGroup(slug string, maxPromptTokens int) (Snapshot, error) {
	if o.ws != nil {
		p, err := o.ws.resolve(o.ws.GroupPath(slug))
		if err != nil {
			return Snapshot{}, err
		}
		content, _, err := readLayerFile(p)
		if err != nil {
			return Snapshot{}, err
		}
		if content != "" {
			return Load(p, maxPromptTokens)
		}
	}
	return o.global.LoadGroup(slug, maxPromptTokens)
}

func (o *Overlay) ShowCurated() (string, error) {
	ws, global, _, err := o.layerContents(func(s *Store) string { return s.CuratedPath() })
	if err != nil {
		return "", err
	}
	if merged := mergeWorkspaceGlobal(ws, global, true); merged != "" {
		return merged, nil
	}
	return emptyMessage, nil
}

func (o *Overlay) ShowDaily() (string, error) {
	now := time.Now()
	ws, global, _, err := o.layerContents(func(s *Store) string { return s.DailyPath(now) })
	if err != nil {
		return "", err
	}
	if merged := mergeWorkspaceGlobal(ws, global, true); merged != "" {
		return merged, nil
	}
	return emptyMessage, nil
}

func (o *Overlay) CuratedSystemPrompt(snapshot Snapshot) string {
	content := strings.TrimSpace(snapshot.Content)
	if content == "" {
		return ""
	}
	if o.ws == nil {
		return SystemPrompt(snapshot)
	}
	return fmt.Sprintf("User memory from %s (workspace) and %s (global):\n%s",
		o.ws.CuratedPath(), o.global.CuratedPath(), content)
}

// Status reports the merged memory status: the token count and
// existence reflect what LoadCurated actually loads (both layers), and
// both layer paths are identified. Reporting only the workspace path
// would claim exists=false/tokens=0 for a fresh workspace whose global
// layer is populated.
func (o *Overlay) Status(maxPromptTokens int, enabled bool) (string, error) {
	snapshot, err := o.LoadCurated(maxPromptTokens)
	if err != nil {
		return "", err
	}
	layers := fmt.Sprintf("path=%s", o.global.CuratedPath())
	if o.ws != nil {
		layers = fmt.Sprintf("workspace=%s global=%s", o.ws.CuratedPath(), o.global.CuratedPath())
	}
	return fmt.Sprintf("memory: enabled=%t %s tokens=%d maxPromptTokens=%d exists=%t truncated=%t",
		enabled, layers, snapshot.Tokens, maxPromptTokens, snapshot.Exists, snapshot.Truncated), nil
}

// PruneWorkspaceDaily deletes expired daily logs from the workspace
// layer and returns the deletion count. A missing (lazily created) layer
// counts as empty; a symlinked layer is rejected rather than pruned.
func (o *Overlay) PruneWorkspaceDaily(retentionDays int) (int, error) {
	if o.ws == nil {
		return 0, nil
	}
	return o.ws.PruneDaily(retentionDays)
}

// Writes default to the workspace layer (created lazily, 0700); the
// *Global variants write to the global layer explicitly.
func (o *Overlay) AppendCurated(text string) error { return o.primary().AppendCurated(text) }
func (o *Overlay) AppendDaily(text string) error   { return o.primary().AppendDaily(text) }
func (o *Overlay) AppendPerson(slug, text string) error {
	return o.primary().AppendPerson(slug, text)
}
func (o *Overlay) AppendGroup(slug, text string) error {
	return o.primary().AppendGroup(slug, text)
}
func (o *Overlay) AppendCuratedGlobal(text string) error { return o.global.AppendCurated(text) }
func (o *Overlay) AppendDailyGlobal(text string) error   { return o.global.AppendDaily(text) }
func (o *Overlay) AppendPersonGlobal(slug, text string) error {
	return o.global.AppendPerson(slug, text)
}
func (o *Overlay) AppendGroupGlobal(slug, text string) error {
	return o.global.AppendGroup(slug, text)
}

func (o *Overlay) SearchLayered(query, scope string) ([]SearchHit, error) {
	// Per-layer results are gathered uncapped: capping before dedup would
	// discard distinct matches that survive it (e.g. ten same-snippet hits
	// collapsing to one).
	var all []SearchHit
	if o.ws != nil {
		hits, err := o.ws.searchAll(query, scope)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			all = append(all, SearchHit{Hit: h, Layer: LayerWorkspace})
		}
	}
	ghits, err := o.global.searchAll(query, scope)
	if err != nil {
		return nil, err
	}
	for _, h := range ghits {
		all = append(all, SearchHit{Hit: h, Layer: LayerGlobal})
	}
	// Dedup across layers only: a global hit is dropped when a workspace
	// hit has the same relative path and identical snippet — the
	// workspace layer shadows the global one. Hits within one layer are
	// never collapsed: distinct matches on different lines are distinct
	// results, even when their snippets are identical.
	wsKeys := make(map[string]struct{})
	for _, h := range all {
		if h.Layer == LayerWorkspace {
			wsKeys[h.Path+"\x00"+h.Snippet] = struct{}{}
		}
	}
	deduped := make([]SearchHit, 0, len(all))
	for _, h := range all {
		if h.Layer == LayerGlobal {
			if _, ok := wsKeys[h.Path+"\x00"+h.Snippet]; ok {
				continue
			}
		}
		deduped = append(deduped, h)
	}
	// Ordering: score first, then workspace wins ties, then file recency —
	// the existing rules with the workspace tie-break added.
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].Score != deduped[j].Score {
			return deduped[i].Score > deduped[j].Score
		}
		if deduped[i].Layer != deduped[j].Layer {
			return deduped[i].Layer == LayerWorkspace
		}
		return deduped[i].mtime > deduped[j].mtime
	})
	if len(deduped) > maxSearchHits {
		deduped = deduped[:maxSearchHits]
	}
	return deduped, nil
}

// The *Store is the single-layer case of the same surface: it satisfies
// ToolStore and StoreAPI so callers can hold either type behind the
// interface. The Global write variants are identical to the plain ones —
// the store itself is the global layer.

// SearchLayered tags every hit with LayerGlobal.
func (s *Store) SearchLayered(query, scope string) ([]SearchHit, error) {
	hits, err := s.Search(query, scope)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{Hit: h, Layer: LayerGlobal})
	}
	return out, nil
}

func (s *Store) AppendCuratedGlobal(text string) error { return s.AppendCurated(text) }
func (s *Store) AppendDailyGlobal(text string) error   { return s.AppendDaily(text) }
func (s *Store) AppendPersonGlobal(slug, text string) error {
	return s.AppendPerson(slug, text)
}
func (s *Store) AppendGroupGlobal(slug, text string) error {
	return s.AppendGroup(slug, text)
}

// LoadPerson loads one person page (no shadowing on a single layer).
func (s *Store) LoadPerson(slug string, maxPromptTokens int) (Snapshot, error) {
	return Load(s.PersonPath(slug), maxPromptTokens)
}

// LoadGroup loads one group page (no shadowing on a single layer).
func (s *Store) LoadGroup(slug string, maxPromptTokens int) (Snapshot, error) {
	return Load(s.GroupPath(slug), maxPromptTokens)
}

func (s *Store) ShowCurated() (string, error) { return Show(s.CuratedPath()) }
func (s *Store) ShowDaily() (string, error)   { return Show(s.DailyPath(time.Now())) }

func (s *Store) CuratedSystemPrompt(snapshot Snapshot) string { return SystemPrompt(snapshot) }

// Status reports this single layer's memory status.
func (s *Store) Status(maxPromptTokens int, enabled bool) (string, error) {
	return Status(s.CuratedPath(), maxPromptTokens, enabled)
}

// Compile-time assertions that both types satisfy the interfaces.
var (
	_ ToolStore = (*Store)(nil)
	_ ToolStore = (*Overlay)(nil)
	_ StoreAPI  = (*Store)(nil)
	_ StoreAPI  = (*Overlay)(nil)
)
