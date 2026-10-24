package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/standingauth"
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
	var providerNames []string
	for _, p := range toolRegistry.Providers() {
		providerNames = append(providerNames, p.Name)
	}
	if want := []string{"", "memory", "kb", "artifact", "connector", "media", "browser", "computer", "mcp"}; !reflect.DeepEqual(providerNames, want) {
		t.Fatalf("pre-C2 registration order changed: %v", providerNames)
	}
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
		var selectedNames []string
		for _, p := range toolRegistry.ForProfile(profile).Providers() {
			selectedNames = append(selectedNames, p.Name)
		}
		if !reflect.DeepEqual(selectedNames, providerNames) {
			t.Fatalf("profile=%q changed pre-C2 registrations", profile)
		}
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

func TestRegistryProfileSelectionAndPlatformGates(t *testing.T) {
	var built, reported []string
	provider := func(name string) ToolProvider {
		return ToolProvider{Name: name, Build: func(context.Context, ToolContext) ([]agent.Tool, error) {
			built = append(built, name)
			return []agent.Tool{tools.NewListTool(t.TempDir())}, nil
		}}
	}
	// Deliberately use a host order different from the built-ins. Selection
	// preserves it; it must not rebuild the list in map iteration order.
	registry := NewRegistry(provider("connector"), provider(""), provider("memory"), provider("host"), provider("mcp"), provider("browser"))
	for _, profile := range []string{"", config.ProfileCoding, config.ProfilePersonal} {
		built, reported = nil, nil
		want := []string{"connector", "", "memory", "host", "mcp", "browser"}
		if profile == config.ProfilePersonal {
			want = []string{"connector", "", "memory", "browser"}
		}
		registry.Build(context.Background(), ToolContext{Config: config.Config{Profile: profile}}, func(name string, err error) {
			if name == "core" {
				name = ""
			}
			reported = append(reported, name)
		})
		if !reflect.DeepEqual(built, want) || !reflect.DeepEqual(reported, want) {
			t.Fatalf("profile=%q builds=%v reports=%v, want %v", profile, built, reported, want)
		}
		var wantCaps []string
		for _, name := range want {
			if name != "" {
				wantCaps = append(wantCaps, name)
			}
		}
		if got := registry.ForProfile(profile).Capabilities(); !reflect.DeepEqual(got, wantCaps) {
			t.Fatalf("profile=%q capabilities=%v, want %v", profile, got, wantCaps)
		}
	}
	registry = NewRegistry(
		ToolProvider{Name: "mcp", Available: func() bool { t.Fatal("disabled provider was probed"); return true }},
		ToolProvider{Name: "browser", Available: func() bool { return false }, Build: func(context.Context, ToolContext) ([]agent.Tool, error) {
			t.Fatal("platform-unavailable provider was built")
			return nil, nil
		}},
	)
	if got := registry.ForProfile(config.ProfilePersonal).Capabilities(); len(got) != 0 {
		t.Fatalf("disabled or unavailable capabilities advertised: %v", got)
	}
	if got := registry.Build(context.Background(), ToolContext{Config: config.Config{Profile: config.ProfilePersonal}}, func(string, error) {
		t.Fatal("skipped provider was reported as attempted")
	}); len(got) != 0 {
		t.Fatalf("disabled or unavailable tools built: %v", got)
	}
}

func TestBuiltinRegistryPersonalFiltering(t *testing.T) {
	cfg := config.Config{
		Profile: config.ProfilePersonal, HomeDir: t.TempDir(), CWD: t.TempDir(), BaseURL: "https://api.example.com",
		Memory: config.MemoryConfig{Enabled: true, Dir: t.TempDir()},
		KBDir:  filepath.Join(t.TempDir(), "no-index"), Artifacts: config.ArtifactConfig{Dir: t.TempDir()},
		Connectors: config.ConnectorConfig{Dir: t.TempDir()},
	}
	store, err := connector.Open(cfg.Connectors.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(google.Name, connector.Token{AccessToken: "at", RefreshToken: "rt", Scopes: google.Scopes}); err != nil {
		t.Fatal(err)
	}
	resources := newConversationTools(Options{Config: cfg})
	t.Cleanup(func() { _ = resources.Close() })
	resources.toolMemo.chromiumResolved, resources.toolMemo.chromiumOK = true, true
	built := resources.Build(context.Background(), cfg, &runtimeProvider{})
	var names []string
	var defs []agent.ToolDefinition
	for _, tool := range built {
		names = append(names, tool.Name())
		defs = append(defs, tool.Definition())
		assertApprovalClassified(t, tool)
	}
	want := []string{
		"read", "list", "grep", "bash", "edit", "write", "subagent",
		"memory_add", "memory_search", "artifact_create", "artifact_edit",
		"gmail_search", "gmail_read", "gmail_send", "calendar_agenda", "calendar_create",
		"browser_navigate", "browser_read", "browser_screenshot",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("personal tools=%v, want %v", names, want)
	}
	if got := resources.Definitions(context.Background(), cfg); !reflect.DeepEqual(got, defs) {
		t.Fatal("personal discovery and execution definitions differ")
	}
	if resources.toolMemo.mediaClient != nil || resources.toolMemo.mcpConnector != nil {
		t.Fatal("disabled media/MCP resources were opened")
	}
	if got := resources.degraded.List(); len(got) != 0 {
		t.Fatalf("disabled providers must not degrade: %+v", got)
	}
	if _, err := os.Stat(standingauth.DefaultDir(cfg.HomeDir)); !os.IsNotExist(err) {
		t.Fatalf("profile assembly created authorization state: %v", err)
	}
	svc := NewService(Options{Config: cfg, ToolProviders: []ToolProvider{}})
	t.Cleanup(func() { _ = svc.Close() })
	if len(svc.buildTools(context.Background(), nil)) != 0 {
		t.Fatal("personal profile re-enabled an explicitly empty registry")
	}
}

func TestUnavailableConnectorsDoNotOpenStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connectors")
	provider := ConnectorTools(ConnectorProvider{Name: "unavailable", Available: func() bool { return false }})
	built, err := provider.Build(context.Background(), ToolContext{Config: config.Config{Connectors: config.ConnectorConfig{Dir: dir}}})
	if err != nil || len(built) != 0 {
		t.Fatalf("unavailable connector built tools or failed: %v, %v", built, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("unavailable connector opened token store: %v", err)
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
