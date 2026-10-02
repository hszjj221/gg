package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/transport/httpapi"
)

// Channel is a long-running daemon background service: the job scheduler,
// a messaging bot, and any future channel. Every channel shares one
// startup and error-reporting path so a new channel cannot invent its own.
type Channel interface {
	// Name identifies the channel in logs and error messages.
	Name() string
	// Run serves until ctx is done. Returning nil on cancellation is
	// normal; any other error is reported to the daemon's logger.
	Run(ctx context.Context) error
}

// Channel state names reported by Monitor.
const (
	ChannelRunning = "running"
	ChannelFailed  = "failed"
	ChannelStopped = "stopped"
)

// ChannelState is the last known runtime state of one channel.
type ChannelState struct {
	State    string
	Err      string
	FailedAt time.Time
}

// Monitor tracks the runtime state of daemon channels. A channel that dies
// at runtime stays dead until the daemon restarts, so its last state is
// exposed here (and surfaced on the authenticated /health endpoint) instead
// of leaving operators to wonder why the bot went quiet.
type Monitor struct {
	mu     sync.Mutex
	states map[string]ChannelState
}

// NewMonitor returns an empty channel monitor.
func NewMonitor() *Monitor {
	return &Monitor{states: make(map[string]ChannelState)}
}

func (m *Monitor) set(name string, state ChannelState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[name] = state
}

// Snapshot returns the current state of every channel launched so far,
// ordered by channel name for stable output.
func (m *Monitor) Snapshot() []httpapi.ChannelStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]httpapi.ChannelStatus, 0, len(m.states))
	for name, st := range m.states {
		cs := httpapi.ChannelStatus{Name: name, State: st.State, Error: st.Err}
		if !st.FailedAt.IsZero() {
			cs.FailedAt = st.FailedAt.Format(time.RFC3339)
		}
		out = append(out, cs)
	}
	// Deterministic order for tests and operators.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type channelDeps struct {
	cfg         config.Config
	rt          *app.Runtime
	logger      *slog.Logger
	noScheduler bool
}

// startChannels constructs and launches every configured channel, returning
// a monitor tracking their runtime state. A construction error is fatal —
// the daemon must not serve half-configured, so every channel is constructed
// before any of them is launched. A channel that fails at runtime is
// reported, marked failed in the monitor, and left stopped; it does not
// take the daemon down with it.
func startChannels(ctx context.Context, deps channelDeps) (*Monitor, error) {
	mon := NewMonitor()
	var channels []Channel
	if !deps.noScheduler {
		ch, err := newSchedulerChannel(deps.cfg, deps.rt, deps.logger)
		if err != nil {
			return nil, fmt.Errorf("scheduler: %w", err)
		}
		channels = append(channels, ch)
	}
	if deps.cfg.TelegramBotToken != "" {
		ch, err := newTelegramChannel(deps.cfg, deps.rt, deps.logger)
		if err != nil {
			return nil, fmt.Errorf("telegram: %w", err)
		}
		channels = append(channels, ch)
	}
	for _, ch := range channels {
		launchChannel(ctx, ch, deps.logger, mon)
	}
	return mon, nil
}

// launchChannel runs ch in the background until ctx is done. A panic in the
// channel is recovered, logged with a stack trace, and recorded as failed:
// one bad channel must never take the scheduler, the HTTP server, and every
// other channel down with it. Runtime failures are reported with the
// channel name; a nil return on context cancellation is silent.
func launchChannel(ctx context.Context, ch Channel, logger *slog.Logger, mon *Monitor) {
	mon.set(ch.Name(), ChannelState{State: ChannelRunning})
	logger.Info("channel starting", "channel", ch.Name())
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				logger.Error("channel panicked", "channel", ch.Name(),
					"panic", fmt.Sprintf("%v", r), "stack", string(stack))
				mon.set(ch.Name(), ChannelState{
					State:    ChannelFailed,
					Err:      fmt.Sprintf("panic: %v", r),
					FailedAt: time.Now(),
				})
			}
		}()
		if err := ch.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("channel failed", "channel", ch.Name(), "error", err)
			mon.set(ch.Name(), ChannelState{
				State:    ChannelFailed,
				Err:      err.Error(),
				FailedAt: time.Now(),
			})
			return
		}
		mon.set(ch.Name(), ChannelState{State: ChannelStopped})
	}()
}
