package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/media"
)

// Bot runs the Telegram long-polling loop and routes updates to the agent.
type Bot struct {
	api      *API
	ws       *app.Runtime
	mclient  *media.Client
	mediaDir string

	allow map[int64]bool

	offsetFile string
	mu         sync.Mutex
	offset     int64
	seen       map[int64]time.Time // update_id -> first seen (replay guard)
	stderr     io.Writer
	logger     *slog.Logger

	// per-chat serialization: one in-flight turn per chat.
	chats   map[int64]*chatState
	chatsMu sync.Mutex
}

type chatState struct {
	mu sync.Mutex
}

// Config wires the bot to the daemon's runtime and media client.
type Config struct {
	Token      string
	AllowChats []int64
	Runtime    *app.Runtime
	Media      *media.Client
	// HomeDir is used for the offset file and voice downloads
	// (~/.gg/telegram/).
	HomeDir string
	// Stderr receives the bot's own log lines (startup banner, ...).
	// Nil defaults to os.Stderr; the daemon injects its stderr writer so
	// all channel output goes through one place.
	Stderr io.Writer
	// Logger, when set, takes over the bot's own log lines (structured).
	// Nil keeps the legacy Stderr lines.
	Logger *slog.Logger
}

// New validates the config and returns a Bot. A nil Media client disables
// the voice pipeline (voice messages get a text-only notice).
func New(cfg Config) (*Bot, error) {
	if cfg.Token == "" {
		return nil, errors.New("telegram: bot token is required")
	}
	if cfg.Runtime == nil {
		return nil, errors.New("telegram: runtime is required")
	}
	if cfg.HomeDir == "" {
		return nil, errors.New("telegram: home dir is required")
	}
	allow := make(map[int64]bool, len(cfg.AllowChats))
	for _, id := range cfg.AllowChats {
		allow[id] = true
	}
	dir := filepath.Join(cfg.HomeDir, ".gg", "telegram")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b := &Bot{
		api:        NewAPI(cfg.Token),
		ws:         cfg.Runtime,
		mclient:    cfg.Media,
		mediaDir:   dir,
		allow:      allow,
		offsetFile: filepath.Join(dir, "offset"),
		seen:       make(map[int64]time.Time),
		chats:      make(map[int64]*chatState),
		stderr:     cfg.Stderr,
		logger:     cfg.Logger,
	}
	if b.stderr == nil {
		b.stderr = os.Stderr
	}
	b.offset = b.loadOffset()
	return b, nil
}

func (b *Bot) loadOffset() int64 {
	data, err := os.ReadFile(b.offsetFile)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (b *Bot) saveOffset(offset int64) {
	b.mu.Lock()
	b.offset = offset
	b.mu.Unlock()
	_ = os.WriteFile(b.offsetFile, []byte(strconv.FormatInt(offset, 10)), 0o600)
}

// allowed reports whether the chat may talk to the bot. An empty allowlist
// means "nobody" — the bot stays silent rather than answering strangers.
func (b *Bot) allowed(chatID int64) bool {
	return b.allow[chatID]
}

// Run starts the long-polling loop; it returns when ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	me, err := b.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram: verify token: %w", err)
	}
	if b.logger != nil {
		b.logger.Info("bot polling", "channel", "telegram", "username", me)
	} else {
		fmt.Fprintf(b.stderr, "telegram: bot @%s polling\n", me)
	}

	// The offset advances only past fully handled updates (at-least-once):
	// a crash re-delivers anything not yet acked instead of losing it.
	// Duplicates are possible after a crash — the in-memory replay guard
	// does not survive restarts — so handling must tolerate re-delivery.
	acker := newOffsetAcker(b.currentOffset(), b.saveOffset)

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		updates, err := b.api.GetUpdates(ctx, acker.frontier(), 30)
		if err != nil {
			var rl *RateLimitedError
			if errors.As(err, &rl) {
				time.Sleep(rl.RetryAfter)
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			if b.isReplay(u.UpdateID) {
				// Already dispatched in this process lifetime; the
				// original worker will ack it. Marking here would ack
				// an update whose handling has not finished.
				continue
			}
			u := u
			acker.add(u.UpdateID)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						b.logPanic("update handler panicked", r, "update_id", u.UpdateID)
						// A panic is deterministic for this input: letting
						// the offset stay would re-deliver the same update
						// and crash again on every restart. Ack the poisoned
						// update so the daemon keeps serving the rest.
						acker.mark(u.UpdateID)
					}
				}()
				b.handleUpdateSync(ctx, u)
				// At-least-once: ack only fully handled updates. If our
				// context was canceled mid-handling the turn did not
				// complete, so leave the offset alone and let the next
				// start re-deliver the update.
				if ctx.Err() == nil {
					acker.mark(u.UpdateID)
				}
			}()
		}
	}
}

// offsetAcker tracks per-update completion and advances a contiguous
// acknowledgment frontier: the persisted offset moves only past updates
// whose handling fully completed. Updates from different chats are handled
// concurrently, so completions can arrive out of order; the offset waits
// for every earlier update before moving.
type offsetAcker struct {
	mu      sync.Mutex
	pending map[int64]bool // updateID -> handled
	next    int64          // smallest unacked updateID
	save    func(int64)
}

func newOffsetAcker(start int64, save func(int64)) *offsetAcker {
	return &offsetAcker{pending: make(map[int64]bool), next: start, save: save}
}

// add registers an update as in-flight.
func (a *offsetAcker) add(updateID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending[updateID] = false
}

// mark records an update as handled and persists the offset past every
// contiguous handled update. Marks for unknown or already-acked updates
// (replays, duplicates) are ignored.
func (a *offsetAcker) mark(updateID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.pending[updateID]; !ok {
		return
	}
	a.pending[updateID] = true
	moved := false
	for a.pending[a.next] {
		delete(a.pending, a.next)
		a.next++
		moved = true
	}
	if moved {
		a.save(a.next)
	}
}

// frontier returns the next expected updateID: the offset GetUpdates
// should poll from.
func (a *offsetAcker) frontier() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

func (b *Bot) currentOffset() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.offset
}

// isReplay reports whether this update_id was already dispatched in this
// process lifetime. Because the offset only advances past fully handled
// updates, a poll that runs before earlier updates finish re-delivers them;
// the guard drops those duplicates so each update is dispatched once.
func (b *Bot) isReplay(updateID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.seen[updateID]; ok {
		return true
	}
	b.seen[updateID] = time.Now()
	// Prune entries older than an hour to bound memory.
	for id, ts := range b.seen {
		if time.Since(ts) > time.Hour {
			delete(b.seen, id)
		}
	}
	return false
}

func (b *Bot) chatState(chatID int64) *chatState {
	b.chatsMu.Lock()
	defer b.chatsMu.Unlock()
	st, ok := b.chats[chatID]
	if !ok {
		st = &chatState{}
		b.chats[chatID] = st
	}
	return st
}

// handleUpdateSync processes one update and returns only after the message
// is fully handled. Updates from the same chat are serialized so two
// messages never interleave agent turns; different chats run concurrently.
// logPanic reports a recovered panic with a stack trace, tolerating a nil
// logger by falling back to stderr like the rest of the bot.
func (b *Bot) logPanic(msg string, r any, args ...any) {
	stack := string(debug.Stack())
	args = append([]any{"panic", fmt.Sprintf("%v", r), "stack", stack}, args...)
	if b.logger != nil {
		b.logger.Error(msg, args...)
		return
	}
	fmt.Fprintf(b.stderr, "telegram: %s panic=%v\n%s\n", msg, r, stack)
}

func (b *Bot) handleUpdateSync(ctx context.Context, u Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	if !b.allowed(msg.Chat.ID) {
		return
	}
	st := b.chatState(msg.Chat.ID)
	st.mu.Lock()
	defer st.mu.Unlock()
	b.handleMessage(ctx, msg)
}

func (b *Bot) handleMessage(ctx context.Context, msg *Message) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	switch {
	case msg.Voice != nil:
		b.handleVoice(ctx, msg)
	case msg.Audio != nil:
		b.handleVoice(ctx, msg)
	case msg.Text != "":
		b.handleText(ctx, msg, msg.Text)
	}
}

func (b *Bot) handleText(ctx context.Context, msg *Message, prompt string) {
	reply, err := b.runAgent(ctx, msg.Chat.ID, prompt)
	if err != nil {
		_ = b.api.SendMessage(ctx, msg.Chat.ID, "出错了："+err.Error())
		return
	}
	_ = b.api.SendMessage(ctx, msg.Chat.ID, reply)
}

// sessionIDForChat maps a Telegram chat to a stable agent session.
func sessionIDForChat(chatID int64) string {
	return fmt.Sprintf("telegram:%d", chatID)
}

// denyAllApprover refuses every approval request: the bot cannot ask the
// user interactively, so tools that need approval are simply unavailable.
type denyAllApprover struct{}

func (denyAllApprover) Approve(_ context.Context, _ agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	return agent.ApprovalDecision{Allow: false}, nil
}

// runAgent runs one agent turn in the chat's session and returns the final
// text.
func (b *Bot) runAgent(ctx context.Context, chatID int64, prompt string) (string, error) {
	sessionID := sessionIDForChat(chatID)
	run, err := b.ws.StartTurnWithApprover(ctx, sessionID, prompt, denyAllApprover{})
	if err != nil {
		return "", err
	}
	var seq int64
	for {
		events, done, err := b.ws.WaitRun(ctx, run.ID(), seq)
		if err != nil {
			return "", err
		}
		for _, ev := range events {
			seq = ev.Sequence
			switch ev.Type {
			case app.EventRunCompleted:
				if ev.Result != nil {
					return ev.Result.Content, nil
				}
				return "", errors.New("empty result")
			case app.EventRunFailed:
				return "", errors.New(ev.Error)
			case app.EventRunCanceled:
				return "", errors.New("canceled")
			}
		}
		if done {
			return "", errors.New("run ended without result")
		}
	}
}
