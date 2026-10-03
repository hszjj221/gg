package app

import (
	"context"
	"errors"
	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type reviewBlockingRepository struct {
	session.Repository
	n             atomic.Int32
	firstLoaded   chan struct{}
	secondLoaded  chan struct{}
	releaseSecond chan struct{}
}

func (r *reviewBlockingRepository) OpenForCWD(cwd, target string, allow bool) (*session.Store, session.Loaded, error) {
	s, l, e := r.Repository.OpenForCWD(cwd, target, allow)
	if e != nil {
		return s, l, e
	}
	switch r.n.Add(1) {
	case 1:
		close(r.firstLoaded)
		<-r.secondLoaded
	case 2:
		close(r.secondLoaded)
		<-r.releaseSecond
	}
	return s, l, e
}

type reviewBlockingProvider struct{ started chan struct{} }

func (p reviewBlockingProvider) Complete(ctx context.Context, _ agent.Request, _ func(agent.Event)) (agent.AssistantMessage, error) {
	close(p.started)
	<-ctx.Done()
	return agent.AssistantMessage{}, ctx.Err()
}
func TestConcurrentOpenPreservesActiveConversation(t *testing.T) {
	rt, _ := runtimeForTest(t)
	snap, err := rt.CreateSession("review")
	if err != nil {
		t.Fatal(err)
	}
	st, err := rt.getState(rt.DefaultWorkspace().ID)
	if err != nil {
		t.Fatal(err)
	}
	st.manager.Remove(snap.SessionID)
	repo := &reviewBlockingRepository{Repository: rt.repository, firstLoaded: make(chan struct{}), secondLoaded: make(chan struct{}), releaseSecond: make(chan struct{})}
	rt.repository = repo
	provider := reviewBlockingProvider{started: make(chan struct{})}
	rt.providerFactory = func(config.Config) agent.Provider { return provider }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opened1 := make(chan error, 1)
	opened2 := make(chan error, 1)
	go func() { _, e := rt.OpenSession(snap.SessionID); opened1 <- e }()
	select {
	case <-repo.firstLoaded:
	case <-ctx.Done():
		t.Fatal("first load timed out")
	}
	go func() { _, e := rt.OpenSession(snap.SessionID); opened2 <- e }()
	select {
	case e := <-opened1:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("first open timed out")
	}
	first, _ := st.manager.Get(snap.SessionID)
	run, e := rt.StartTurn(ctx, snap.SessionID, "in progress", false)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("provider timed out")
	}
	close(repo.releaseSecond)
	select {
	case e := <-opened2:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("second open timed out")
	}
	second, _ := st.manager.Get(snap.SessionID)
	if first != second {
		t.Fatal("concurrent open replaced the registered service")
	}
	if len(second.Snapshot().Messages) == 0 {
		t.Fatal("active history was lost")
	}
	if _, active := st.manager.ActiveRun(snap.SessionID); !active {
		t.Fatal("active run was lost")
	}
	second.mu.Lock()
	running := second.running
	second.mu.Unlock()
	if !running {
		t.Fatal("active service unexpectedly marked idle")
	}
	run.Cancel()
	waitForRun(t, run, nil)
}

func TestManagerCloseCancelsRunsAndRejectsAdmission(t *testing.T) {
	m, id := runtimeTestManager(t, &runtimeProvider{block: true})
	service, _ := m.Get(id)
	run, err := m.StartTurn(context.Background(), id, "blocked", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.browserPool.Get(ctx); err == nil {
		t.Fatal("closed service can restart browser")
	}
	if !run.Status().Done {
		t.Fatal("Close returned before worker finished")
	}
	if _, err = m.StartTurn(ctx, id, "new", false); err == nil {
		t.Fatal("closed manager accepted a run")
	}
	if _, ok := m.Get(id); ok {
		t.Fatal("closed service retained")
	}
	if err = m.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestManagerRunDeadlineAndSharedAdmission(t *testing.T) {
	limiter := newRunLimiter(1)
	opts := ManagerOptions{RunTimeout: 50 * time.Millisecond, limiter: limiter}
	a, idA := runtimeTestManagerWithOptions(t, &runtimeProvider{block: true}, opts)
	b, idB := runtimeTestManagerWithOptions(t, &runtimeProvider{}, opts)
	defer a.Close(context.Background())
	defer b.Close(context.Background())
	run, err := a.StartTurn(context.Background(), idA, "blocked", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.StartTurn(context.Background(), idB, "over capacity", false); err == nil {
		t.Fatal("shared limit exceeded")
	} else {
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Code != ErrorRunCapacity || !appErr.Retryable {
			t.Fatalf("wrong capacity error: %v", err)
		}
	}
	events := waitForRun(t, run, nil)
	if last := events[len(events)-1]; last.Type != EventRunFailed || !strings.Contains(last.Error, "deadline exceeded") {
		t.Fatalf("deadline did not terminate run: %+v", last)
	}
	retry, err := b.StartTurn(context.Background(), idB, "retry", false)
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, retry, nil)
}
func TestRunRingWrapAndByteBudget(t *testing.T) {
	run := newRun("session", func() {}, 3, time.Now())
	run.maxEventBytes = 2048
	for i := 0; i < 20; i++ {
		run.publish(Event{Type: EventAgent, Agent: &agent.Event{Type: agent.EventTextDelta, Text: "small"}})
	}
	events, _, err := run.Wait(context.Background(), 17)
	if err != nil || len(events) != 3 || events[0].Sequence != 18 || events[2].Sequence != 20 {
		t.Fatalf("ring wrap: %+v %v", events, err)
	}
	run.publish(Event{Type: EventAgent, Agent: &agent.Event{Text: strings.Repeat("x", 4096)}})
	if run.eventBytes > run.maxEventBytes || run.eventCount != 0 {
		t.Fatal("oversized event retained")
	}
	if _, _, err = run.Wait(context.Background(), 20); err == nil {
		t.Fatal("oversize did not expire history")
	}
	run.publish(Event{Type: EventAgent, Agent: &agent.Event{Text: "resumed"}})
	events, _, err = run.Wait(context.Background(), 21)
	if err != nil || len(events) != 1 || events[0].Sequence != 22 {
		t.Fatalf("resume after eviction: %v %v", events, err)
	}
}

func TestRuntimeSharesCapacityAndClosesAllWorkspaces(t *testing.T) {
	rt, _ := runtimeForTest(t)
	a, err := rt.getState(rt.DefaultWorkspace().ID)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := rt.registry.Add("other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.getState(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.manager.options.limiter != b.manager.options.limiter {
		t.Fatal("workspace admission is not shared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = rt.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = rt.getState(ws.ID); err == nil {
		t.Fatal("closed runtime creates workspace state")
	}
	for _, m := range []*Manager{a.manager, b.manager} {
		m.mu.Lock()
		closed := m.closed
		m.mu.Unlock()
		if !closed {
			t.Fatal("workspace manager left open")
		}
	}
}
