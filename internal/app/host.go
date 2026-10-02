package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
	"github.com/hszjj221/gg/internal/workspace"
)

// workspaceState is the per-workspace slice of runtime state. Every
// registered workspace gets its own conversation manager, skill set, and
// degraded-provider registry, so sessions, tool discovery, and provider
// health stay isolated by workspace root. States are created lazily on
// first use, except the default workspace's, which is built eagerly at
// startup.
type workspaceState struct {
	ws       workspace.Workspace
	manager  *Manager
	skills   skills.Set
	degraded *DegradedRegistry
	// memStore is the per-workspace memory overlay: <root>/.gg/memory/
	// searched before (and shadowing) the global store. Built in getState.
	memStore memory.StoreAPI
}

type RuntimeOptions struct {
	Config          config.Config
	ProviderFactory ProviderFactory
	// WorkspaceRegistry resolves every session to a workspace. Required:
	// the daemon startup hook persists the default registration into it.
	WorkspaceRegistry *workspace.Registry
	// NoSkills disables per-workspace skill discovery; every workspace
	// then runs with an empty skill set.
	NoSkills      bool
	Repository    session.Repository
	Manager       ManagerOptions
	Profile       userprofile.Profile
	MemoryStore   *memory.Store
	ArtifactStore *artifact.Store
	LibraryStore  *library.Store
	// Log is passed to conversation services so tool provider build
	// failures are visible in daemon logs. Nil means slog.Default().
	Log *slog.Logger
}

// Runtime is the application host used by non-terminal transports. It
// exposes stable IDs and deliberately keeps filesystem paths private.
//
// The workspace registry is treated as immutable after NewRuntime returns —
// the daemon is single-instance and nothing in the runtime mutates it — so
// concurrent readers need no lock around it. Per-workspace states are
// guarded by mu.
type Runtime struct {
	cfg             config.Config
	providerFactory ProviderFactory
	repository      session.Repository
	profile         userprofile.Profile
	memStore        *memory.Store
	artifacts       *artifact.Store
	libraryStore    *library.Store
	logger          *slog.Logger

	registry         *workspace.Registry
	noSkills         bool
	managerOptions   ManagerOptions
	defaultWorkspace workspace.Workspace

	mu     sync.Mutex
	states map[string]*workspaceState
}

type SessionSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount int    `json:"messageCount"`
	Preview      string `json:"preview"`
}

func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("session repository is required")
	}
	if options.ProviderFactory == nil {
		return nil, fmt.Errorf("provider factory is required")
	}
	if options.WorkspaceRegistry == nil {
		return nil, fmt.Errorf("workspace registry is required")
	}
	// The registry canonicalizes roots; absolutize the configured CWD the
	// same way the daemon startup hook does so EnsureDefault matches the
	// registration it persisted.
	cwd, err := filepath.Abs(options.Config.CWD)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	def, _, err := options.WorkspaceRegistry.EnsureDefault(cwd)
	if err != nil {
		return nil, fmt.Errorf("ensure default workspace: %w", err)
	}
	w := &Runtime{
		cfg:              options.Config,
		providerFactory:  options.ProviderFactory,
		repository:       options.Repository,
		profile:          options.Profile,
		memStore:         options.MemoryStore,
		artifacts:        options.ArtifactStore,
		libraryStore:     options.LibraryStore,
		logger:           options.Log,
		registry:         options.WorkspaceRegistry,
		noSkills:         options.NoSkills,
		managerOptions:   options.Manager,
		defaultWorkspace: def,
		states:           make(map[string]*workspaceState),
	}
	// Build the default workspace's state eagerly so startup surfaces
	// skill-discovery failures instead of deferring them to first use.
	if _, err := w.getState(def.ID); err != nil {
		return nil, err
	}
	return w, nil
}

// getState returns the state for workspaceID, creating it on first use.
// Creation is single-flight under mu. A skill-discovery failure is
// returned without caching the state, so the next call retries.
func (w *Runtime) getState(workspaceID string) (*workspaceState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st, ok := w.states[workspaceID]; ok {
		return st, nil
	}
	ws, ok := w.registry.FindByID(workspaceID)
	if !ok {
		return nil, errorf(ErrorWorkspaceMismatch, false, "workspace %q is not registered", workspaceID)
	}
	st := &workspaceState{ws: ws, degraded: &DegradedRegistry{}}
	// Each workspace gets its own memory overlay: <root>/.gg/memory/
	// searched before (and shadowing) the global store. The overlay
	// directory is created lazily on first write; reads never create it.
	global := w.memStore
	if global == nil {
		global = memory.NewStore(w.cfg.Memory.Dir)
	}
	st.memStore = memory.NewOverlay(memory.OverlayDir(ws.Root), global)
	// Prune expired workspace daily logs at state init (the global store
	// is pruned in SetupPersonal). A missing lazily-created layer counts
	// as empty; failures are warnings, not fatal.
	if ov, ok := st.memStore.(*memory.Overlay); ok {
		if _, err := ov.PruneWorkspaceDaily(w.cfg.Memory.DailyLogRetentionDays); err != nil && w.logger != nil {
			w.logger.Warn("prune workspace daily memory", "workspace", ws.Name, "error", err)
		}
	}
	if !w.noSkills {
		skillSet, err := skills.Load(skills.LoadOptions{CWD: ws.Root, HomeDir: w.cfg.HomeDir})
		if err != nil {
			// Skill discovery errors can carry the workspace's absolute
			// path; scrub it before the error crosses the JSON-RPC
			// boundary (same ~ rule PR #38 applies to provider build
			// failures). The full error goes to the daemon log.
			if w.logger != nil {
				w.logger.Error("load skills for workspace", "workspace", ws.Name, "error", err)
			}
			return nil, fmt.Errorf("load skills for workspace %q: %s", ws.Name, sanitizeProviderReason(w.cfg.HomeDir, err.Error()))
		}
		st.skills = skillSet
	}
	st.manager = NewManagerWithOptions(w.managerOptions)
	w.states[workspaceID] = st
	return st, nil
}

// allStates snapshots the per-workspace states created so far. The result
// must be used without holding mu; each state's manager is independently
// thread-safe.
func (w *Runtime) allStates() []*workspaceState {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*workspaceState, 0, len(w.states))
	for _, st := range w.states {
		out = append(out, st)
	}
	return out
}

// workspaceForSession resolves the workspace a loaded session belongs to,
// backfilling the workspace ID into the session header when the session
// predates workspace bindings (or was matched by root).
func (w *Runtime) workspaceForSession(store *session.Store, loaded session.Loaded) (workspace.Workspace, error) {
	ws, backfill, err := w.registry.ResolveForSession(loaded.Header.WorkspaceID, loaded.Header.CWD)
	if err != nil {
		return workspace.Workspace{}, err
	}
	if backfill {
		if err := store.SetWorkspaceID(ws.ID); err != nil {
			return workspace.Workspace{}, err
		}
	}
	return ws, nil
}

// WorkspaceRegistry exposes the workspace registry for channel wiring
// (Telegram chat mapping, scheduler jobs).
func (w *Runtime) WorkspaceRegistry() *workspace.Registry { return w.registry }

// DefaultWorkspace returns the workspace sessions land in when no
// workspace is specified.
func (w *Runtime) DefaultWorkspace() workspace.Workspace { return w.defaultWorkspace }

// ResolveWorkspace maps a user-facing workspace reference to a workspace:
// empty selects the default; otherwise the ID is tried first, then the
// name.
func (w *Runtime) ResolveWorkspace(ref string) (workspace.Workspace, error) {
	if ref == "" {
		return w.defaultWorkspace, nil
	}
	if ws, ok := w.registry.FindByID(ref); ok {
		return ws, nil
	}
	if ws, ok := w.registry.FindByName(ref); ok {
		return ws, nil
	}
	return workspace.Workspace{}, errorf(ErrorWorkspaceMismatch, false, "workspace %q not found", ref)
}

func (w *Runtime) ListSessions() ([]SessionSummary, error) {
	// Session listing stays scoped to the default workspace; aggregating
	// across workspaces is follow-up work.
	infos, err := w.repository.List(w.defaultWorkspace.Root)
	if err != nil {
		return nil, err
	}
	result := make([]SessionSummary, 0, len(infos))
	for _, info := range infos {
		result = append(result, SessionSummary{
			ID:           info.ID,
			Name:         info.Name,
			UpdatedAt:    info.Timestamp,
			MessageCount: info.MessageCount,
			Preview:      info.Preview,
		})
	}
	return result, nil
}

func (w *Runtime) CreateSession(name string) (Snapshot, error) {
	return w.CreateSessionInWorkspace(name, "")
}

// CreateSessionInWorkspace creates a session in the workspace named by
// workspaceRef (see ResolveWorkspace); an empty ref selects the default
// workspace. The new session is bound to the workspace in its header.
func (w *Runtime) CreateSessionInWorkspace(name, workspaceRef string) (Snapshot, error) {
	ws, err := w.ResolveWorkspace(workspaceRef)
	if err != nil {
		return Snapshot{}, err
	}
	store, _, err := w.repository.Create(ws.Root)
	if err != nil {
		return Snapshot{}, err
	}
	if err := store.SetWorkspaceID(ws.ID); err != nil {
		return Snapshot{}, err
	}
	loaded := store.State()
	if name != "" {
		if err := store.AppendName(name); err != nil {
			return Snapshot{}, err
		}
		loaded = store.State()
	}
	_, service, err := w.addLoaded(store, loaded)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Runtime) OpenSession(sessionID string) (Snapshot, error) {
	_, service, err := w.service(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Runtime) Snapshot(sessionID string) (Snapshot, error) {
	_, service, err := w.service(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Runtime) RenameSession(sessionID, name string) (Snapshot, error) {
	st, service, err := w.service(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := service.RenameSession(name); err != nil {
		return Snapshot{}, w.sessionError(st, sessionID, err)
	}
	return service.Snapshot(), nil
}

func (w *Runtime) SessionAction(sessionID string, action SessionAction, entryID string) (SessionUpdate, error) {
	st, service, err := w.service(sessionID)
	if err != nil {
		return SessionUpdate{}, err
	}
	switch action {
	case SessionActionTree:
		update, err := service.Checkout(entryID)
		return update, w.sessionError(st, sessionID, err)
	case SessionActionFork:
		child, update, err := service.Fork(entryID)
		if err != nil {
			return SessionUpdate{}, w.sessionError(st, sessionID, err)
		}
		if _, err := st.manager.Add(child); err != nil {
			return SessionUpdate{}, err
		}
		return update, nil
	case SessionActionClone:
		child, update, err := service.Clone()
		if err != nil {
			return SessionUpdate{}, w.sessionError(st, sessionID, err)
		}
		if _, err := st.manager.Add(child); err != nil {
			return SessionUpdate{}, err
		}
		return update, nil
	default:
		return SessionUpdate{}, errorf(ErrorInvalidAction, false, "unknown session action %q", action)
	}
}

func (w *Runtime) StartTurn(ctx context.Context, sessionID, prompt string, requireApproval bool) (*Run, error) {
	return w.StartTurnInWorkspace(ctx, sessionID, prompt, requireApproval, "")
}

// StartTurnWithApprover starts a turn with an explicit approver; see
// Manager.StartTurnWithApprover.
func (w *Runtime) StartTurnWithApprover(ctx context.Context, sessionID, prompt string, approver agent.Approver) (*Run, error) {
	return w.StartTurnWithApproverInWorkspace(ctx, sessionID, prompt, approver, "")
}

// StartTurnInWorkspace starts a turn like StartTurn, but scoped to the
// workspace named by workspaceRef. An empty ref keeps the legacy behavior:
// the session is found in whatever workspace holds it. A non-empty ref
// pins the session to that workspace: a session already open under another
// workspace is reported by workspace name only — never by path.
func (w *Runtime) StartTurnInWorkspace(ctx context.Context, sessionID, prompt string, requireApproval bool, workspaceRef string) (*Run, error) {
	st, _, err := w.serviceInWorkspace(sessionID, workspaceRef)
	if err != nil {
		return nil, err
	}
	return st.manager.StartTurn(ctx, sessionID, prompt, requireApproval)
}

// StartTurnWithApproverInWorkspace is StartTurnInWorkspace with an explicit
// approver.
func (w *Runtime) StartTurnWithApproverInWorkspace(ctx context.Context, sessionID, prompt string, approver agent.Approver, workspaceRef string) (*Run, error) {
	st, _, err := w.serviceInWorkspace(sessionID, workspaceRef)
	if err != nil {
		return nil, err
	}
	return st.manager.StartTurnWithApprover(ctx, sessionID, prompt, approver)
}

// serviceInWorkspace resolves the session like service, but a non-empty
// workspaceRef pins the lookup to that workspace instead of searching all
// of them.
func (w *Runtime) serviceInWorkspace(sessionID, workspaceRef string) (*workspaceState, *Service, error) {
	if workspaceRef == "" {
		return w.service(sessionID)
	}
	ws, err := w.ResolveWorkspace(workspaceRef)
	if err != nil {
		return nil, nil, err
	}
	st, err := w.getState(ws.ID)
	if err != nil {
		return nil, nil, err
	}
	if service, ok := st.manager.Get(sessionID); ok {
		return st, service, nil
	}
	// The session may already be open under a different workspace: name
	// that workspace only. Roots are private to the daemon host and must
	// never leak into client-facing errors.
	for _, other := range w.allStates() {
		if other == st {
			continue
		}
		if _, ok := other.manager.Get(sessionID); ok {
			return nil, nil, errorf(ErrorWorkspaceMismatch, false,
				"session %q belongs to workspace %q", sessionID, other.ws.Name)
		}
	}
	store, loaded, err := w.repository.OpenForCWD(ws.Root, sessionID, false)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, nil, wrapError(ErrorSessionNotFound, false, err, "session %q not found", sessionID)
		}
		return nil, nil, err
	}
	resolved, err := w.workspaceForSession(store, loaded)
	if err != nil {
		return nil, nil, err
	}
	if resolved.ID != ws.ID {
		return nil, nil, errorf(ErrorWorkspaceMismatch, false,
			"session %q belongs to workspace %q", sessionID, resolved.Name)
	}
	return w.addLoaded(store, loaded)
}

func (w *Runtime) WaitRun(ctx context.Context, runID string, afterSequence int64) ([]Event, bool, error) {
	for _, st := range w.allStates() {
		if run, ok := st.manager.Run(runID); ok {
			return run.Wait(ctx, afterSequence)
		}
	}
	return nil, false, errorf(ErrorRunNotFound, false, "run %q not found", runID)
}

func (w *Runtime) RunStatus(runID string) (RunStatus, error) {
	for _, st := range w.allStates() {
		if status, ok := st.manager.RunStatus(runID); ok {
			return status, nil
		}
	}
	return RunStatus{}, errorf(ErrorRunNotFound, false, "run %q not found", runID)
}

func (w *Runtime) ActiveRun(sessionID string) (*RunStatus, error) {
	st, _, err := w.service(sessionID)
	if err != nil {
		return nil, err
	}
	status, ok := st.manager.ActiveRun(sessionID)
	if !ok {
		return nil, nil
	}
	return &status, nil
}

func (w *Runtime) CancelRun(runID string) error {
	for _, st := range w.allStates() {
		if _, ok := st.manager.Run(runID); ok {
			return st.manager.Cancel(runID)
		}
	}
	return errorf(ErrorRunNotFound, false, "run %q not found", runID)
}

func (w *Runtime) Approve(runID, approvalID string, allow bool) error {
	for _, st := range w.allStates() {
		if _, ok := st.manager.Run(runID); ok {
			return st.manager.Approve(runID, approvalID, agent.ApprovalDecision{Allow: allow})
		}
	}
	return errorf(ErrorRunNotFound, false, "run %q not found", runID)
}

func (w *Runtime) Steer(sessionID, text string, followUp bool) error {
	st, _, err := w.service(sessionID)
	if err != nil {
		return err
	}
	return st.manager.Steer(sessionID, text, followUp)
}

// service returns the workspace state and conversation service for a
// session, opening the session in whichever workspace holds it when it is
// not already open.
func (w *Runtime) service(sessionID string) (*workspaceState, *Service, error) {
	for _, st := range w.allStates() {
		if service, ok := st.manager.Get(sessionID); ok {
			return st, service, nil
		}
	}
	return w.openSessionAnywhere(sessionID)
}

// openSessionAnywhere opens a session by ID in whichever registered
// workspace holds it, trying workspaces in registration order. Sessions
// created before workspace bindings existed are backfilled on open.
func (w *Runtime) openSessionAnywhere(sessionID string) (*workspaceState, *Service, error) {
	var notFound error
	var firstErr error
	for _, ws := range w.registry.List() {
		store, loaded, err := w.repository.OpenForCWD(ws.Root, sessionID, false)
		if err != nil {
			if errors.Is(err, session.ErrNotFound) {
				notFound = err
				continue
			}
			// A corrupt session file (or unreadable directory) in one
			// workspace must not make sessions in later workspaces
			// unreachable: remember it and keep probing.
			if firstErr == nil {
				firstErr = err
			}
			if w.logger != nil {
				w.logger.Warn("probe session in workspace", "workspace", ws.Name, "session", sessionID, "error", err)
			}
			continue
		}
		return w.addLoaded(store, loaded)
	}
	if firstErr != nil {
		return nil, nil, firstErr
	}
	if notFound != nil {
		return nil, nil, wrapError(ErrorSessionNotFound, false, notFound, "session %q not found", sessionID)
	}
	return nil, nil, errorf(ErrorSessionNotFound, false, "session %q not found", sessionID)
}

// DegradedProviders returns the union of tool providers that failed their
// most recent build across all workspace states, deduplicated by provider
// name and sorted. Every session reports both failures and recoveries into
// its workspace's registry, so a success in one session clears a failure
// recorded by another within that workspace. Failure reasons are already
// path-scrubbed at report time, so the union leaks no workspace roots.
func (w *Runtime) DegradedProviders() []DegradedProvider {
	seen := make(map[string]DegradedProvider)
	for _, st := range w.allStates() {
		for _, dp := range st.degraded.List() {
			if _, ok := seen[dp.Name]; !ok {
				seen[dp.Name] = dp
			}
		}
	}
	out := make([]DegradedProvider, 0, len(seen))
	for _, dp := range seen {
		out = append(out, dp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (w *Runtime) addLoaded(store *session.Store, loaded session.Loaded) (*workspaceState, *Service, error) {
	ws, err := w.workspaceForSession(store, loaded)
	if err != nil {
		return nil, nil, err
	}
	st, err := w.getState(ws.ID)
	if err != nil {
		return nil, nil, err
	}
	// The service's root comes from the workspace, not the process:
	// tools run sandboxed to the workspace the session belongs to.
	cfg := w.cfg
	cfg.CWD = ws.Root
	if loaded.LastModel != nil {
		cfg, err = cfg.WithSelection(loaded.LastModel.Selection)
		if err != nil {
			return nil, nil, err
		}
	}
	service := NewService(Options{
		Config:          cfg,
		ProviderFactory: w.providerFactory,
		Store:           store,
		History:         loaded.Messages,
		Summary:         loaded.LastSummary,
		Skills:          st.skills,
		ModelRecorded:   loaded.LastModel != nil && loaded.LastModel.Selection == cfg.Selection,
		Profile:         w.profile,
		MemoryStore:     st.memStore,
		Log:             w.logger,
		Degraded:        st.degraded,
	})
	if _, err := st.manager.Add(service); err != nil {
		return nil, nil, err
	}
	return st, service, nil
}

func (w *Runtime) sessionError(st *workspaceState, sessionID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, session.ErrConflict) || errors.Is(err, session.ErrLocked) {
		st.manager.Remove(sessionID)
		return wrapError(ErrorSessionConflict, true, err, "session %q changed in another process; reopen and retry", sessionID)
	}
	return err
}
