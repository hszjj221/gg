package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/contextmgr"
)

func (s *Service) handleControlCommand(ctx context.Context, prompt string) (Result, bool, error) {
	if result, ok, err := s.handleModelCommand(prompt); ok || err != nil {
		return result, ok, err
	}
	if result, ok, err := s.handleMemoryCommand(prompt); ok || err != nil {
		return result, ok, err
	}
	if ok, err := parseNoArgCommand(prompt, "/compact"); ok || err != nil {
		if err != nil {
			return Result{}, true, err
		}
		if err := s.ensureModelRecorded(); err != nil {
			return Result{}, true, err
		}
		if s.providerFactory == nil {
			return Result{}, true, fmt.Errorf("provider factory is not configured")
		}
		compact, err := s.compactHistory(ctx, s.providerFactory(s.cfg))
		if err != nil {
			return Result{}, true, err
		}
		return Result{Content: compact.message, Usage: compact.usage, ModelName: s.cfg.Selection}, true, nil
	}
	if ok, err := parseNoArgCommand(prompt, "/context"); ok || err != nil {
		if err != nil {
			return Result{}, true, err
		}
		return Result{Content: s.contextStatus(ctx), ModelName: s.cfg.Selection}, true, nil
	}
	return Result{}, false, nil
}

func (s *Service) handleModelCommand(prompt string) (Result, bool, error) {
	arg, ok, err := parseModelCommand(prompt)
	if !ok || err != nil {
		return Result{}, ok, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if arg == "" {
		return Result{Content: s.modelListTextLocked(), ModelName: s.cfg.Selection}, true, nil
	}
	next, err := s.cfg.WithSelection(arg)
	if err != nil {
		return Result{}, true, err
	}
	if err := s.selectModelLocked(next); err != nil {
		return Result{}, true, err
	}
	return Result{Content: "model switched to " + s.cfg.Selection, ModelName: s.cfg.Selection}, true, nil
}

// modelListTextLocked requires s.mu to be held.
func (s *Service) modelListTextLocked() string {
	var b strings.Builder
	fmt.Fprintf(&b, "current model: %s", s.cfg.Selection)
	selections := s.cfg.AvailableSelections()
	if len(selections) == 0 {
		return b.String()
	}
	b.WriteString("\navailable models:")
	for _, selection := range selections {
		fmt.Fprintf(&b, "\n- %s", selection)
	}
	return b.String()
}

func (s *Service) handleMemoryCommand(prompt string) (Result, bool, error) {
	command, arg, ok, err := parseMemoryCommand(prompt)
	if !ok || err != nil {
		return Result{}, ok, err
	}
	if !s.cfg.Memory.Enabled {
		return Result{}, true, fmt.Errorf("memory is disabled")
	}
	store := s.memStore
	if command == "" {
		status, err := store.Status(s.cfg.Memory.MaxPromptTokens, s.cfg.Memory.Enabled)
		return Result{Content: status, ModelName: s.cfg.Selection}, true, err
	}
	switch command {
	case "add":
		scope, target, text := parseMemoryFlags(arg)
		if strings.TrimSpace(text) == "" {
			return Result{}, true, fmt.Errorf("usage: /memory add [--scope=SCOPE] [--target=workspace|global] <text>")
		}
		var toGlobal bool
		switch target {
		case "", "workspace":
		case "global":
			toGlobal = true
		default:
			return Result{}, true, fmt.Errorf("unknown target %q: want workspace or global", target)
		}
		// On a plain (global-only) store the Global variants are
		// identical to the plain ones, so target is naturally ignored
		// without a workspace context.
		appendCurated := store.AppendCurated
		appendDaily := store.AppendDaily
		appendPerson := store.AppendPerson
		appendGroup := store.AppendGroup
		if toGlobal {
			appendCurated = store.AppendCuratedGlobal
			appendDaily = store.AppendDailyGlobal
			appendPerson = store.AppendPersonGlobal
			appendGroup = store.AppendGroupGlobal
		}
		var werr error
		switch {
		case scope == "" || scope == "general":
			werr = appendCurated(text)
		case scope == "daily":
			werr = appendDaily(text)
		case strings.HasPrefix(scope, "person:"):
			name := strings.TrimSpace(strings.TrimPrefix(scope, "person:"))
			if name == "" {
				return Result{}, true, fmt.Errorf("person scope needs a name: /memory add --scope=person:<name> <text>")
			}
			werr = appendPerson(name, text)
		case strings.HasPrefix(scope, "group:"):
			name := strings.TrimSpace(strings.TrimPrefix(scope, "group:"))
			if name == "" {
				return Result{}, true, fmt.Errorf("group scope needs a name: /memory add --scope=group:<name> <text>")
			}
			werr = appendGroup(name, text)
		default:
			return Result{}, true, fmt.Errorf("unknown scope %q: want general, daily, person:<name>, group:<name>", scope)
		}
		if werr != nil {
			return Result{}, true, werr
		}
		return Result{Content: "memory added", ModelName: s.cfg.Selection}, true, nil
	case "show":
		// Show the merged view (workspace layer first, then global) —
		// the same content the prompt snapshot carries.
		var content string
		var err error
		if arg == "daily" {
			content, err = store.ShowDaily()
		} else {
			content, err = store.ShowCurated()
		}
		return Result{Content: content, ModelName: s.cfg.Selection}, true, err
	case "search":
		hits, err := store.SearchLayered(arg, "all")
		if err != nil {
			return Result{}, true, err
		}
		if len(hits) == 0 {
			return Result{Content: "no memory matches", ModelName: s.cfg.Selection}, true, nil
		}
		var b strings.Builder
		for _, hit := range hits {
			fmt.Fprintf(&b, "[%s] %s:%d: %s\n", hit.Layer, hit.Path, hit.Line, hit.Snippet)
		}
		return Result{Content: strings.TrimSpace(b.String()), ModelName: s.cfg.Selection}, true, nil
	default:
		return Result{}, true, fmt.Errorf("usage: /memory [add [--scope=SCOPE] [--target=workspace|global] <text>|show [daily]|search <query>]")
	}
}

// parseMemoryFlags splits leading "--scope=SCOPE" / "--target=TARGET"
// flags from /memory add arguments.
func parseMemoryFlags(arg string) (scope, target, text string) {
	rest := arg
	for {
		head, tail, ok := strings.Cut(rest, " ")
		if !ok {
			break
		}
		if v, ok := strings.CutPrefix(head, "--scope="); ok {
			scope = v
		} else if v, ok := strings.CutPrefix(head, "--target="); ok {
			target = v
		} else {
			break
		}
		rest = strings.TrimSpace(tail)
	}
	// A bare "--scope=X" / "--target=Y" with no text is a usage error,
	// not literal content.
	if v, ok := strings.CutPrefix(rest, "--scope="); ok {
		scope, text = v, ""
	} else if v, ok := strings.CutPrefix(rest, "--target="); ok {
		target, text = v, ""
	} else {
		text = rest
	}
	return scope, target, text
}

func (s *Service) contextStatus(ctx context.Context) string {
	system, err := s.systemMessages()
	if err != nil {
		return "context: " + err.Error()
	}
	build := s.buildContext(system, agent.Message{})
	var defs []agent.ToolDefinition
	for _, tool := range s.buildTools(ctx, nil) {
		defs = append(defs, tool.Definition())
	}
	hasSummary := s.summary != nil && strings.TrimSpace(s.summary.Summary) != ""
	return fmt.Sprintf(
		"context: promptTokens=%d maxPromptTokens=%d tailTurns=%d summary=%t autoCompact=%t maxOutputTokens=%d",
		build.PromptTokens+contextmgr.EstimateTools(defs),
		s.cfg.Context.MaxPromptTokens,
		s.cfg.Context.TailTurns,
		hasSummary,
		s.cfg.Context.AutoCompact,
		s.cfg.Context.MaxOutputTokens,
	)
}
