package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/artifact"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/library"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
)

type WorkspaceOptions struct {
	Config          config.Config
	ProviderFactory ProviderFactory
	Skills          skills.Set
	Repository      session.Repository
	Manager         ManagerOptions
	Profile         userprofile.Profile
	MemoryStore     *memory.Store
	ArtifactStore   *artifact.Store
	LibraryStore    *library.Store
	// Log is passed to conversation services so tool provider build
	// failures are visible in daemon logs. Nil means slog.Default().
	Log *slog.Logger
}

// Workspace is the application facade used by non-terminal transports. It
// exposes stable IDs and deliberately keeps filesystem paths private.
type Workspace struct {
	cfg             config.Config
	providerFactory ProviderFactory
	skills          skills.Set
	repository      session.Repository
	manager         *Manager
	profile         userprofile.Profile
	memStore        *memory.Store
	artifacts       *artifact.Store
	libraryStore    *library.Store
	logger          *slog.Logger
	// degraded is shared by every conversation service in the workspace:
	// all sessions report provider build outcomes into it, so a provider
	// that recovers in any session is cleared everywhere instead of
	// lingering as a stale failure from a session that hasn't run since.
	degraded *DegradedRegistry
}

type SessionSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	UpdatedAt    string `json:"updatedAt"`
	MessageCount int    `json:"messageCount"`
	Preview      string `json:"preview"`
}

func NewWorkspace(options WorkspaceOptions) (*Workspace, error) {
	if options.Repository == nil {
		return nil, fmt.Errorf("session repository is required")
	}
	if options.ProviderFactory == nil {
		return nil, fmt.Errorf("provider factory is required")
	}
	return &Workspace{
		cfg:             options.Config,
		providerFactory: options.ProviderFactory,
		skills:          options.Skills,
		repository:      options.Repository,
		manager:         NewManagerWithOptions(options.Manager),
		profile:         options.Profile,
		memStore:        options.MemoryStore,
		artifacts:       options.ArtifactStore,
		libraryStore:    options.LibraryStore,
		logger:          options.Log,
		degraded:        &DegradedRegistry{},
	}, nil
}

func (w *Workspace) ListSessions() ([]SessionSummary, error) {
	infos, err := w.repository.List(w.cfg.CWD)
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

func (w *Workspace) CreateSession(name string) (Snapshot, error) {
	store, loaded, err := w.repository.Create(w.cfg.CWD)
	if err != nil {
		return Snapshot{}, err
	}
	if name != "" {
		if err := store.AppendName(name); err != nil {
			return Snapshot{}, err
		}
		loaded = store.State()
	}
	service, err := w.addLoaded(store, loaded)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Workspace) OpenSession(sessionID string) (Snapshot, error) {
	if service, ok := w.manager.Get(sessionID); ok {
		return service.Snapshot(), nil
	}
	store, loaded, err := w.repository.OpenForCWD(w.cfg.CWD, sessionID, false)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return Snapshot{}, wrapError(ErrorSessionNotFound, false, err, "session %q not found", sessionID)
		}
		return Snapshot{}, err
	}
	service, err := w.addLoaded(store, loaded)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Workspace) Snapshot(sessionID string) (Snapshot, error) {
	service, err := w.service(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	return service.Snapshot(), nil
}

func (w *Workspace) RenameSession(sessionID, name string) (Snapshot, error) {
	service, err := w.service(sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := service.RenameSession(name); err != nil {
		return Snapshot{}, w.sessionError(sessionID, err)
	}
	return service.Snapshot(), nil
}

func (w *Workspace) SessionAction(sessionID string, action SessionAction, entryID string) (SessionUpdate, error) {
	service, err := w.service(sessionID)
	if err != nil {
		return SessionUpdate{}, err
	}
	switch action {
	case SessionActionTree:
		update, err := service.Checkout(entryID)
		return update, w.sessionError(sessionID, err)
	case SessionActionFork:
		child, update, err := service.Fork(entryID)
		if err != nil {
			return SessionUpdate{}, w.sessionError(sessionID, err)
		}
		if _, err := w.manager.Add(child); err != nil {
			return SessionUpdate{}, err
		}
		return update, nil
	case SessionActionClone:
		child, update, err := service.Clone()
		if err != nil {
			return SessionUpdate{}, w.sessionError(sessionID, err)
		}
		if _, err := w.manager.Add(child); err != nil {
			return SessionUpdate{}, err
		}
		return update, nil
	default:
		return SessionUpdate{}, errorf(ErrorInvalidAction, false, "unknown session action %q", action)
	}
}

func (w *Workspace) StartTurn(ctx context.Context, sessionID, prompt string, requireApproval bool) (*Run, error) {
	if _, err := w.service(sessionID); err != nil {
		return nil, err
	}
	return w.manager.StartTurn(ctx, sessionID, prompt, requireApproval)
}

// StartTurnWithApprover starts a turn with an explicit approver; see
// Manager.StartTurnWithApprover.
func (w *Workspace) StartTurnWithApprover(ctx context.Context, sessionID, prompt string, approver agent.Approver) (*Run, error) {
	if _, err := w.service(sessionID); err != nil {
		return nil, err
	}
	return w.manager.StartTurnWithApprover(ctx, sessionID, prompt, approver)
}

func (w *Workspace) WaitRun(ctx context.Context, runID string, afterSequence int64) ([]Event, bool, error) {
	run, ok := w.manager.Run(runID)
	if !ok {
		return nil, false, errorf(ErrorRunNotFound, false, "run %q not found", runID)
	}
	return run.Wait(ctx, afterSequence)
}

func (w *Workspace) RunStatus(runID string) (RunStatus, error) {
	status, ok := w.manager.RunStatus(runID)
	if !ok {
		return RunStatus{}, errorf(ErrorRunNotFound, false, "run %q not found", runID)
	}
	return status, nil
}

func (w *Workspace) ActiveRun(sessionID string) (*RunStatus, error) {
	if _, err := w.service(sessionID); err != nil {
		return nil, err
	}
	status, ok := w.manager.ActiveRun(sessionID)
	if !ok {
		return nil, nil
	}
	return &status, nil
}

func (w *Workspace) CancelRun(runID string) error {
	return w.manager.Cancel(runID)
}

func (w *Workspace) Approve(runID, approvalID string, allow bool) error {
	return w.manager.Approve(runID, approvalID, agent.ApprovalDecision{Allow: allow})
}

func (w *Workspace) Steer(sessionID, text string, followUp bool) error {
	if _, err := w.service(sessionID); err != nil {
		return err
	}
	return w.manager.Steer(sessionID, text, followUp)
}

func (w *Workspace) service(sessionID string) (*Service, error) {
	if service, ok := w.manager.Get(sessionID); ok {
		return service, nil
	}
	if _, err := w.OpenSession(sessionID); err != nil {
		return nil, err
	}
	service, ok := w.manager.Get(sessionID)
	if !ok {
		return nil, fmt.Errorf("session %q could not be opened", sessionID)
	}
	return service, nil
}

// DegradedProviders returns the tool providers that failed their most
// recent build in any open session, with reasons. Every session reports
// both failures and recoveries into the shared registry, so a success in
// one session clears a failure recorded by another — a fixed provider
// stops being reported as degraded without every session having to run.
// Empty means every build since the last failure succeeded.
func (w *Workspace) DegradedProviders() []DegradedProvider {
	return w.degraded.List()
}

func (w *Workspace) addLoaded(store *session.Store, loaded session.Loaded) (*Service, error) {
	cfg := w.cfg
	var err error
	if loaded.LastModel != nil {
		cfg, err = cfg.WithSelection(loaded.LastModel.Selection)
		if err != nil {
			return nil, err
		}
	}
	service := NewService(Options{
		Config:          cfg,
		ProviderFactory: w.providerFactory,
		Store:           store,
		History:         loaded.Messages,
		Summary:         loaded.LastSummary,
		Skills:          w.skills,
		ModelRecorded:   loaded.LastModel != nil && loaded.LastModel.Selection == cfg.Selection,
		Profile:         w.profile,
		MemoryStore:     w.memStore,
		Log:             w.logger,
		Degraded:        w.degraded,
	})
	if _, err := w.manager.Add(service); err != nil {
		return nil, err
	}
	return service, nil
}

func (w *Workspace) sessionError(sessionID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, session.ErrConflict) || errors.Is(err, session.ErrLocked) {
		w.manager.Remove(sessionID)
		return wrapError(ErrorSessionConflict, true, err, "session %q changed in another process; reopen and retry", sessionID)
	}
	return err
}
