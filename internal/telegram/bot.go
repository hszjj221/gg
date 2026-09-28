package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
	ws       *app.Workspace
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

// Config wires the bot to the daemon's workspace and media client.
type Config struct {
	Token      string
	AllowChats []int64
	Workspace  *app.Workspace
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
	if cfg.Workspace == nil {
		return nil, errors.New("telegram: workspace is required")
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
		ws:         cfg.Workspace,
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

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		updates, err := b.api.GetUpdates(ctx, b.currentOffset(), 30)
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
			b.saveOffset(u.UpdateID + 1)
			if b.isReplay(u.UpdateID) {
				continue
			}
			b.handleUpdate(ctx, u)
		}
	}
}

func (b *Bot) currentOffset() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.offset
}

// isReplay reports whether this update_id was seen recently (a stale update
// re-delivered after a restart with a lost offset file).
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

func (b *Bot) handleUpdate(ctx context.Context, u Update) {
	msg := u.Message
	if msg == nil {
		return
	}
	if !b.allowed(msg.Chat.ID) {
		return
	}
	st := b.chatState(msg.Chat.ID)
	// Serialize per chat so two messages from the same chat never interleave
	// agent turns; different chats run concurrently.
	go func() {
		st.mu.Lock()
		defer st.mu.Unlock()
		b.handleMessage(ctx, msg)
	}()
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
