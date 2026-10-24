package app

import (
	"context"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/connector"
	"github.com/hszjj221/gg/internal/connector/google"
	"github.com/hszjj221/gg/internal/tools"
)

// googleConnector is the statically linked first-party Connector SPI pilot.
// OAuth, token refresh and tool execution stay in their existing packages.
func googleConnector() ConnectorProvider {
	return ConnectorProvider{Name: google.Name, Build: buildGoogleTools}
}

func buildGoogleTools(ctx context.Context, tc ToolContext, cstore *connector.Store, tok connector.Token) ([]agent.Tool, error) {
	gclient, err := google.NewClient(cstore, google.Config{
		ClientID:     tc.Config.Connectors.Google.ClientID,
		ClientSecret: tc.Config.Connectors.Google.ClientSecret,
	})
	if err != nil {
		return nil, nil
	}
	// Register only the tools the user actually granted: a partial
	// (granular-consent) grant must not advertise tools that would just 403.
	var out []agent.Tool
	if hasScope(tok.Scopes, google.ScopeGmailRead) {
		out = append(out,
			tools.NewGmailSearchTool(gclient),
			tools.NewGmailReadTool(gclient),
		)
	}
	if hasScope(tok.Scopes, google.ScopeGmailSend) || hasScope(tok.Scopes, google.ScopeGmailCompose) {
		mail := tools.NewGmailSendTool(gclient)
		mail.DraftByDefault = tc.Config.Profile == config.ProfilePersonal
		out = append(out, mail)
	}
	if hasScope(tok.Scopes, google.ScopeCalendar) {
		out = append(out,
			tools.NewCalendarAgendaTool(gclient, tc.Location, tc.LocationErr),
			tools.NewCalendarCreateTool(gclient, tc.Location, tc.LocationErr),
		)
	}
	return out, nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
