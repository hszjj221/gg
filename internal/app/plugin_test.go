package app

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/tools"
)

func TestRegistrySnapshotsHostRegistrations(t *testing.T) {
	providers := []ToolProvider{{Name: "host", Build: buildCoreTools}}
	registry := NewRegistry(providers...)
	providers[0].Name = "changed"
	registry.Providers()[0].Name = "also changed"
	if got := registry.Capabilities(); !reflect.DeepEqual(got, []string{"host"}) {
		t.Fatalf("host mutation changed registry: %v", got)
	}
	// An empty snapshot must remain non-nil when passed through Options;
	// nil selects the built-ins, including when a conversation is forked.
	if (Registry{}).Providers() == nil {
		t.Fatal("empty registry would re-enable default tools through Options")
	}
}

func TestBuiltinRegistryCodingCompatibility(t *testing.T) {
	cfg := config.Config{
		HomeDir: t.TempDir(), CWD: t.TempDir(), BaseURL: "https://api.example.com",
		KBDir:      filepath.Join(t.TempDir(), "no-index"),
		Memory:     config.MemoryConfig{Enabled: true, Dir: t.TempDir()},
		Artifacts:  config.ArtifactConfig{Dir: t.TempDir()},
		Connectors: config.ConnectorConfig{Dir: t.TempDir()},
	}
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(google.Name, connector.Token{
		AccessToken: "at", RefreshToken: "rt", Scopes: google.Scopes,
	}); err != nil {
		t.Fatal(err)
	}
	// Lock the pre-C1 order with optional capabilities enabled. KB and MCP
	// have no configured source; browser construction is lazy and needs no process.
	want := []string{
		"read", "list", "grep", "bash", "edit", "write", "subagent",
		"memory_add", "memory_search", "artifact_create", "artifact_edit",
		"gmail_search", "gmail_read", "gmail_send", "calendar_agenda", "calendar_create",
		"image_generate", "tts", "stt", "browser_navigate", "browser_read", "browser_screenshot",
	}
	if computerToolsSupported() {
		want = append(want, "computer_info", "process_list", "process_kill", "open", "notify", "clipboard_read", "clipboard_write")
	}
	var codingDefs []agent.ToolDefinition
	for _, profile := range []string{"", config.ProfileCoding} {
		cfg.Profile = profile
		resources := newConversationTools(Options{Config: cfg})
		t.Cleanup(func() { _ = resources.Close() })
		resources.toolMemo.chromiumResolved, resources.toolMemo.chromiumOK = true, true
		built := resources.Build(context.Background(), cfg, &runtimeProvider{})
		names := make([]string, 0, len(built))
		defs := make([]agent.ToolDefinition, 0, len(built))
		for _, tool := range built {
			names = append(names, tool.Name())
			defs = append(defs, tool.Definition())
		}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("profile=%q tools=%v, want %v", profile, names, want)
		}
		if got := resources.Definitions(context.Background(), cfg); !reflect.DeepEqual(got, defs) {
			t.Fatalf("profile=%q discovery and execution definitions differ", profile)
		}
		if profile == "" {
			codingDefs = defs
		} else if !reflect.DeepEqual(defs, codingDefs) {
			t.Fatal("unset and explicit coding schemas differ")
		}
		if got := resources.degraded.List(); len(got) != 0 {
			t.Fatalf("unexpected degraded providers: %+v", got)
		}
	}
}

func TestGoogleConnectorScopesThroughSPI(t *testing.T) {
	cfg := config.Config{CWD: t.TempDir(), Connectors: config.ConnectorConfig{Dir: t.TempDir()}}
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		t.Fatal(err)
	}
	client, err := google.NewClient(store, google.Config{})
	if err != nil {
		t.Fatal(err)
	}
	search, read := tools.NewGmailSearchTool(client), tools.NewGmailReadTool(client)
	send := tools.NewGmailSendTool(client)
	agenda, create := tools.NewCalendarAgendaTool(client, time.Local, nil), tools.NewCalendarCreateTool(client, time.Local, nil)
	svc := NewService(Options{Config: cfg, ToolProviders: []ToolProvider{ConnectorTools(googleConnector())}})
	t.Cleanup(func() { _ = svc.Close() })
	for _, tc := range []struct {
		name   string
		scopes []string
		want   []agent.Tool
	}{
		{"no scopes", nil, nil},
		{"read", []string{google.ScopeGmailRead}, []agent.Tool{search, read}},
		{"send", []string{google.ScopeGmailSend}, []agent.Tool{send}},
		{"compose", []string{google.ScopeGmailCompose}, []agent.Tool{send}},
		{"calendar", []string{google.ScopeCalendar}, []agent.Tool{agenda, create}},
		{"all", google.Scopes, []agent.Tool{search, read, send, agenda, create}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Save(google.Name, connector.Token{AccessToken: "at", RefreshToken: "rt", Scopes: tc.scopes}); err != nil {
				t.Fatal(err)
			}
			got := svc.buildTools(context.Background(), nil)
			if len(got) != len(tc.want) {
				t.Fatalf("built %d tools, want %d", len(got), len(tc.want))
			}
			for i, tool := range got {
				if !reflect.DeepEqual(tool.Definition(), tc.want[i].Definition()) {
					t.Fatalf("tool %d definition changed: %+v", i, tool.Definition())
				}
			}
		})
	}
	if err := store.Remove(google.Name); err != nil {
		t.Fatal(err)
	}
	if got := svc.buildTools(context.Background(), nil); len(got) != 0 || len(svc.DegradedProviders()) != 0 {
		t.Fatalf("disconnect must silently remove tools on the next turn: %v", got)
	}
}

func TestConnectorSPIHostInjection(t *testing.T) {
	cfg := config.Config{CWD: t.TempDir(), Connectors: config.ConnectorConfig{Dir: t.TempDir()}}
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		t.Fatal(err)
	}
	token := connector.Token{AccessToken: "host-at", RefreshToken: "host-rt", Scopes: []string{"host-scope"}}
	if err := store.Save("host", token); err != nil {
		t.Fatal(err)
	}
	providers := []ConnectorProvider{
		{Name: "unavailable", Available: func() bool { return false }},
		{Name: "not-connected"},
		{Name: "host", Build: func(ctx context.Context, tc ToolContext, store *connector.Store, tok connector.Token) ([]agent.Tool, error) {
			if tc.Config.CWD != cfg.CWD || store.Dir() != cfg.Connectors.Dir || !reflect.DeepEqual(tok, token) {
				t.Fatal("connector did not receive host context and stored token")
			}
			return []agent.Tool{tools.NewReadToolWithOptions(tc.Config.CWD, tools.ReadOptions{})}, nil
		}},
	}
	provider := ConnectorTools(providers...)
	providers[2].Build = nil // Registration snapshots the host's slice.
	svc := NewService(Options{Config: cfg, ToolProviders: []ToolProvider{provider}})
	t.Cleanup(func() { _ = svc.Close() })
	if got := svc.buildTools(context.Background(), nil); len(got) != 1 || got[0].Name() != "read" {
		t.Fatalf("host connector tools = %v", got)
	}
}
