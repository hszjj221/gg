package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/app"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/session"
	"github.com/hszjj221/gg/internal/workspace"
)

func TestChatStoreRoundTrip(t *testing.T) {
	home := t.TempDir()
	s, err := loadChatStore(home)
	if err != nil {
		t.Fatal(err)
	}
	// Unknown chat yields the zero binding.
	if got := s.get(123); got != (chatBinding{}) {
		t.Fatalf("unknown chat binding = %+v", got)
	}
	if err := s.set(123, chatBinding{WorkspaceID: "w_1", SessionID: "sess_1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.set(-456, chatBinding{WorkspaceID: "w_2"}); err != nil {
		t.Fatal(err)
	}
	// Reload from disk: bindings survive.
	s2, err := loadChatStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.get(123); got != (chatBinding{WorkspaceID: "w_1", SessionID: "sess_1"}) {
		t.Fatalf("reloaded binding = %+v", got)
	}
	if got := s2.get(-456); got != (chatBinding{WorkspaceID: "w_2"}) {
		t.Fatalf("reloaded binding = %+v", got)
	}
	// The store is 0600.
	fi, err := os.Stat(filepath.Join(home, ".gg", "channels", "telegram.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("chat store mode = %o, want 600", fi.Mode().Perm())
	}
	// The channels dir is 0700.
	di, err := os.Stat(filepath.Join(home, ".gg", "channels"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("channels dir mode = %o, want 700", di.Mode().Perm())
	}
	// On-disk shape: {"version":1,"chats":{...}} with string keys.
	data, err := os.ReadFile(filepath.Join(home, ".gg", "channels", "telegram.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Version int                        `json:"version"`
		Chats   map[string]json.RawMessage `json:"chats"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Version != 1 {
		t.Fatalf("version = %d, want 1", raw.Version)
	}
	if _, ok := raw.Chats["123"]; !ok {
		t.Fatalf("chat key %q missing: %s", "123", data)
	}
	if _, ok := raw.Chats["-456"]; !ok {
		t.Fatalf("chat key %q missing: %s", "-456", data)
	}
}

func TestChatStoreOverwrites(t *testing.T) {
	home := t.TempDir()
	s, err := loadChatStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.set(7, chatBinding{WorkspaceID: "w_1", SessionID: "s_1"}); err != nil {
		t.Fatal(err)
	}
	// Switching workspaces clears the session: the stored binding is
	// replaced wholesale.
	if err := s.set(7, chatBinding{WorkspaceID: "w_2"}); err != nil {
		t.Fatal(err)
	}
	got := s.get(7)
	if got.WorkspaceID != "w_2" || got.SessionID != "" {
		t.Fatalf("after switch = %+v, want workspace w_2 with cleared session", got)
	}
}

func TestParseWorkspaceCommand(t *testing.T) {
	cases := []struct {
		in    string
		arg   string
		isCmd bool
	}{
		{"/workspace", "", true},
		{"/workspace ", "", true},
		{"/workspace  work ", "work", true},
		{"/workspace my project", "my project", true},
		{"/workspace@gg_bot", "", true},
		{"/workspace@gg_bot other", "other", true},
		{"hello", "", false},
		{"/workspacely", "", false},
		{"/workspace2", "", false},
		{" /workspace", "", false},
		{"/WORKSPACE", "", false},
	}
	for _, tc := range cases {
		arg, isCmd := parseWorkspaceCommand(tc.in)
		if isCmd != tc.isCmd || arg != tc.arg {
			t.Errorf("parseWorkspaceCommand(%q) = (%q, %v), want (%q, %v)",
				tc.in, arg, isCmd, tc.arg, tc.isCmd)
		}
	}
}

// testRuntime builds a minimal app.Runtime with two registered workspaces:
// the default (covering cfg CWD) and a second one named "second".
func testRuntime(t *testing.T, home string) (*app.Runtime, workspace.Workspace, workspace.Workspace) {
	t.Helper()
	root := t.TempDir()
	reg, err := workspace.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	defDir := filepath.Join(root, "default")
	secondDir := filepath.Join(root, "second")
	for _, d := range []string{defDir, secondDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.Add("second", secondDir); err != nil {
		t.Fatal(err)
	}
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CWD: defDir, Selection: "test:model", HomeDir: home}
	rt, err := app.NewRuntime(app.RuntimeOptions{
		Config:            cfg,
		ProviderFactory:   func(config.Config) agent.Provider { return nil },
		WorkspaceRegistry: reg,
		NoSkills:          true,
		Repository:        session.NewFileRepository(filepath.Join(root, "sessions")),
	})
	if err != nil {
		t.Fatal(err)
	}
	def := rt.DefaultWorkspace()
	second, ok := reg.FindByName("second")
	if !ok {
		t.Fatal("second workspace missing")
	}
	return rt, def, second
}

func TestWorkspaceCommandSwitchClearsSession(t *testing.T) {
	home := t.TempDir()
	rt, def, second := testRuntime(t, home)

	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.FormValue("text"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	store, err := loadChatStore(home)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{api: newAPIForURL("tok", srv.URL), ws: rt, chatStore: store}

	const chatID = int64(99)
	if err := store.set(chatID, chatBinding{WorkspaceID: def.ID, SessionID: "stale-session"}); err != nil {
		t.Fatal(err)
	}

	// Query reports the current workspace name.
	b.handleWorkspaceCommand(context.Background(), chatID, "")
	if len(sent) != 1 || sent[0] != "当前 workspace："+def.Name {
		t.Fatalf("query reply = %q", sent)
	}
	// The query must not touch the stored session.
	if got := store.get(chatID); got.SessionID != "stale-session" {
		t.Fatalf("query changed session: %+v", got)
	}

	// Unknown workspace: binding untouched, error reply names the input.
	b.handleWorkspaceCommand(context.Background(), chatID, "nope")
	if len(sent) != 2 || sent[1] != `未找到 workspace "nope"` {
		t.Fatalf("unknown reply = %q", sent)
	}
	if got := store.get(chatID); got.WorkspaceID != def.ID || got.SessionID != "stale-session" {
		t.Fatalf("unknown switch changed binding: %+v", got)
	}

	// Switch: session cleared, reply names the target, no paths leak.
	b.handleWorkspaceCommand(context.Background(), chatID, "second")
	if len(sent) != 3 || sent[2] != `已切换到 workspace "second"` {
		t.Fatalf("switch reply = %q", sent)
	}
	got := store.get(chatID)
	if got.WorkspaceID != second.ID {
		t.Fatalf("workspace not switched: %+v", got)
	}
	if got.SessionID != "" {
		t.Fatalf("session not cleared after switch: %+v", got)
	}
	for _, s := range sent {
		if filepath.IsAbs(s) || (len(s) > 1 && s[1] == ':') {
			t.Fatalf("reply leaks a path: %q", s)
		}
	}
}
func TestChatStoreSetRollsBackOnPersistFailure(t *testing.T) {
	// Point the store at a path whose directory does not exist: the persist
	// fails, and the in-memory view must stay on the old binding.
	s := &chatStore{
		path:  filepath.Join(t.TempDir(), "no-such-dir", "telegram.json"),
		chats: map[string]chatBinding{chatKey(7): {WorkspaceID: "ws-old", SessionID: "s-old"}},
	}
	if err := s.set(7, chatBinding{WorkspaceID: "ws-new", SessionID: "s-new"}); err == nil {
		t.Fatal("set with unwritable path = nil, want error")
	}
	if got := s.get(7); got.WorkspaceID != "ws-old" || got.SessionID != "s-old" {
		t.Fatalf("in-memory binding after failed set = %+v, want old binding", got)
	}
}

func TestChatStoreSetPersistsBeforeSwap(t *testing.T) {
	dir := t.TempDir()
	s, err := loadChatStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.set(7, chatBinding{WorkspaceID: "ws-new", SessionID: "s-new"}); err != nil {
		t.Fatal(err)
	}
	// Reload from disk: the persisted binding must match the in-memory one.
	reloaded, err := loadChatStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.get(7); got.WorkspaceID != "ws-new" || got.SessionID != "s-new" {
		t.Fatalf("reloaded binding = %+v, want new binding", got)
	}
}

func TestRepairBindingDropsStaleSession(t *testing.T) {
	home := t.TempDir()
	rt, def, _ := testRuntime(t, home)
	store, err := loadChatStore(home)
	if err != nil {
		t.Fatal(err)
	}
	b := &Bot{ws: rt, chatStore: store}

	const chatID = int64(42)
	// Bound to a workspace that no longer exists, with a live session id:
	// the repair must move the chat to the default workspace and drop the
	// session instead of resuming it there.
	if err := store.set(chatID, chatBinding{WorkspaceID: "ws-gone", SessionID: "s-old"}); err != nil {
		t.Fatal(err)
	}
	got := b.repairBinding(chatID)
	if got.WorkspaceID != def.ID || got.SessionID != "" {
		t.Fatalf("repaired = %+v, want default workspace with no session", got)
	}
	if stored := store.get(chatID); stored.WorkspaceID != def.ID || stored.SessionID != "" {
		t.Fatalf("stored after repair = %+v, want default workspace with no session", stored)
	}
	// A healthy binding is untouched.
	if err := store.set(chatID, chatBinding{WorkspaceID: def.ID, SessionID: "s-keep"}); err != nil {
		t.Fatal(err)
	}
	if got := b.repairBinding(chatID); got.WorkspaceID != def.ID || got.SessionID != "s-keep" {
		t.Fatalf("healthy binding changed: %+v", got)
	}
	// An empty workspace reference repairs to default without a session.
	if err := store.set(chatID, chatBinding{}); err != nil {
		t.Fatal(err)
	}
	if got := b.repairBinding(chatID); got.WorkspaceID != def.ID || got.SessionID != "" {
		t.Fatalf("empty binding repaired = %+v", got)
	}
}
