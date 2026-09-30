package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
)

// Computer tools give the agent high-level access to the local machine —
// system info, process management, opening files/URLs, notifications, and
// the clipboard — without making the model assemble shell commands.
//
// Platform specifics live in computer_darwin.go / computer_linux.go /
// computer_windows.go. The backends below are the seam:
//
//	computerInfoText(ctx) (string, error)
//	listProcesses(ctx) ([]processInfo, error)
//	killProcess(pid int, force bool) error
//	openTarget(ctx, target string) error
//	sendNotification(ctx, title, body string) error
//	readClipboard(ctx) (string, error)
//	writeClipboard(ctx, text string) error
//
// Safety levels (per the architecture design):
//   - computer_info, process_list, notify: read-only or harmless, no approval.
//   - process_kill, open, clipboard_read, clipboard_write: approval-gated via
//     agent.ApprovalDescriber.

const (
	maxComputerOutputRunes = 10000
	maxProcessListLimit    = 200
	// computerCommandTimeout bounds every subprocess a computer tool
	// spawns; the turn context still applies on top of it.
	computerCommandTimeout = 30 * time.Second
)

type processInfo struct {
	PID  int
	Name string
	CPU  float64 // percent of one CPU
	Mem  float64 // percent of physical memory
}

func boundedComputerCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, computerCommandTimeout)
}

// parsePS parses `ps -eo pid,comm,pcpu,pmem` output. The comm column may
// contain spaces (Linux allows them via PR_SET_NAME), so the numeric columns
// are parsed from the ends of the row: pid is the first field, %CPU/%MEM the
// last two, and everything between is the command name. The header line fails
// the pid parse and is skipped.
func parsePS(out string) []processInfo {
	var procs []processInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(fields[len(fields)-2], 64)
		mem, _ := strconv.ParseFloat(fields[len(fields)-1], 64)
		procs = append(procs, processInfo{
			PID:  pid,
			Name: strings.Join(fields[1:len(fields)-2], " "),
			CPU:  cpu,
			Mem:  mem,
		})
	}
	return procs
}

// ---------------------------------------------------------------------------
// computer_info
// ---------------------------------------------------------------------------

type ComputerInfoTool struct{}

func NewComputerInfoTool() ComputerInfoTool { return ComputerInfoTool{} }

func (t ComputerInfoTool) Name() string { return "computer_info" }

func (t ComputerInfoTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "computer_info",
		Description: "Show local machine information: OS, architecture, hostname, CPU count, memory, and disk usage. Read-only.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t ComputerInfoTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	info, err := computerInfoText(ctx)
	if err != nil {
		return errorResult(fmt.Errorf("computer_info: %w", err))
	}
	return textResult(truncateRunes(info, maxComputerOutputRunes))
}

// ---------------------------------------------------------------------------
// process_list
// ---------------------------------------------------------------------------

type ProcessListTool struct{}

func NewProcessListTool() ProcessListTool { return ProcessListTool{} }

func (t ProcessListTool) Name() string { return "process_list" }

func (t ProcessListTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "process_list",
		Description: "List running processes with PID, CPU%, memory%, and command name, sorted by CPU or memory usage. Read-only.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":   map[string]any{"type": "integer", "description": "max processes to return (default 50, max 200)"},
				"sort_by": map[string]any{"type": "string", "description": "\"cpu\" or \"mem\" (default \"cpu\")"},
			},
		},
	}
}

func (t ProcessListTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Limit  int    `json:"limit"`
		SortBy string `json:"sort_by"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid process_list arguments: %w", err))
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxProcessListLimit {
		limit = maxProcessListLimit
	}
	sortBy := strings.ToLower(strings.TrimSpace(input.SortBy))
	if sortBy != "mem" {
		sortBy = "cpu"
	}

	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	procs, err := listProcesses(ctx)
	if err != nil {
		return errorResult(fmt.Errorf("process_list: %w", err))
	}
	if sortBy == "mem" {
		sort.Slice(procs, func(i, j int) bool { return procs[i].Mem > procs[j].Mem })
	} else {
		sort.Slice(procs, func(i, j int) bool { return procs[i].CPU > procs[j].CPU })
	}
	if len(procs) > limit {
		procs = procs[:limit]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %-7s %-7s %s\n", "PID", "CPU%", "MEM%", "COMMAND")
	for _, p := range procs {
		fmt.Fprintf(&b, "%-8d %-7.1f %-7.1f %s\n", p.PID, p.CPU, p.Mem, p.Name)
	}
	return textResult(truncateRunes(b.String(), maxComputerOutputRunes))
}

// ---------------------------------------------------------------------------
// process_kill (approval-gated)
// ---------------------------------------------------------------------------

type ProcessKillTool struct{}

func NewProcessKillTool() ProcessKillTool { return ProcessKillTool{} }

func (t ProcessKillTool) Name() string { return "process_kill" }

func (t ProcessKillTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "process_kill",
		Description: "Terminate a process by PID. Defaults to SIGTERM; use signal \"kill\" (SIGKILL) only when the process ignores termination. Requires approval.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pid":    map[string]any{"type": "integer", "description": "process ID to terminate"},
				"signal": map[string]any{"type": "string", "description": "\"term\" (default) or \"kill\""},
			},
			"required": []string{"pid"},
		},
	}
}

func parseProcessKillInput(raw json.RawMessage) (pid int, force bool, err error) {
	var input struct {
		PID    int    `json:"pid"`
		Signal string `json:"signal"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return 0, false, fmt.Errorf("invalid process_kill arguments: %w", err)
	}
	if input.PID <= 1 {
		return 0, false, fmt.Errorf("refusing to kill pid %d", input.PID)
	}
	if input.PID == os.Getpid() {
		return 0, false, fmt.Errorf("refusing to kill the agent's own process (pid %d)", input.PID)
	}
	switch strings.ToLower(strings.TrimSpace(input.Signal)) {
	case "", "term":
		return input.PID, false, nil
	case "kill":
		return input.PID, true, nil
	default:
		return 0, false, fmt.Errorf("signal must be \"term\" or \"kill\"")
	}
}

func (t ProcessKillTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	pid, force, err := parseProcessKillInput(raw)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	sig := "SIGTERM"
	if force {
		sig = "SIGKILL"
	}
	return agent.ApprovalRequest{
		ToolName:  "process_kill",
		Summary:   fmt.Sprintf("kill process %d (%s)", pid, sig),
		Details:   fmt.Sprintf("pid: %d\nsignal: %s", pid, sig),
		Arguments: raw,
	}, nil
}

func (t ProcessKillTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	pid, force, err := parseProcessKillInput(raw)
	if err != nil {
		return errorResult(err)
	}
	if err := killProcess(pid, force); err != nil {
		return errorResult(fmt.Errorf("process_kill %d: %w", pid, err))
	}
	sig := "SIGTERM"
	if force {
		sig = "SIGKILL"
	}
	return textResult(fmt.Sprintf("sent %s to process %d", sig, pid))
}

// ---------------------------------------------------------------------------
// open (approval-gated)
// ---------------------------------------------------------------------------

type OpenTool struct {
	// cwd is the agent workspace; relative file targets resolve against it
	// instead of the gg process's own working directory (which differs for
	// ggd started with --cwd elsewhere).
	cwd string
}

func NewOpenTool(cwd string) OpenTool { return OpenTool{cwd: cwd} }

func (t OpenTool) Name() string { return "open" }

func (t OpenTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "open",
		Description: "Open a file or URL with the system's default application. Requires approval: the target is shown to the user before opening.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "file path or URL to open"},
			},
			"required": []string{"target"},
		},
	}
}

func parseOpenInput(raw json.RawMessage) (string, error) {
	var input struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("invalid open arguments: %w", err)
	}
	target := strings.TrimSpace(input.Target)
	if target == "" {
		return "", fmt.Errorf("target is required")
	}
	if len([]rune(target)) > 2048 {
		return "", fmt.Errorf("target is too long")
	}
	return target, nil
}

var urlSchemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// resolveTarget resolves a relative file target against the workspace.
// URLs (anything with a scheme) and absolute paths pass through untouched.
func (t OpenTool) resolveTarget(target string) string {
	if t.cwd == "" || filepath.IsAbs(target) || urlSchemeRE.MatchString(target) {
		return target
	}
	return filepath.Join(t.cwd, target)
}

func (t OpenTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	target, err := parseOpenInput(raw)
	if err != nil {
		return agent.ApprovalRequest{}, err
	}
	resolved := t.resolveTarget(target)
	return agent.ApprovalRequest{
		ToolName:  "open",
		Summary:   "open with default application: " + resolved,
		Details:   "target: " + resolved,
		Arguments: raw,
	}, nil
}

func (t OpenTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	target, err := parseOpenInput(raw)
	if err != nil {
		return errorResult(err)
	}
	resolved := t.resolveTarget(target)
	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	if err := openTarget(ctx, resolved); err != nil {
		return errorResult(fmt.Errorf("open: %w", err))
	}
	return textResult("opened: " + resolved)
}

// ---------------------------------------------------------------------------
// notify
// ---------------------------------------------------------------------------

type NotifyTool struct{}

func NewNotifyTool() NotifyTool { return NotifyTool{} }

func (t NotifyTool) Name() string { return "notify" }

func (t NotifyTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "notify",
		Description: "Show a system notification with a title and body. Harmless; no approval needed.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
			},
			"required": []string{"title", "body"},
		},
	}
}

func (t NotifyTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid notify arguments: %w", err))
	}
	title := truncateRunes(strings.TrimSpace(input.Title), 200)
	body := truncateRunes(strings.TrimSpace(input.Body), 2000)
	if title == "" && body == "" {
		return errorResult(fmt.Errorf("title or body is required"))
	}
	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	if err := sendNotification(ctx, title, body); err != nil {
		return errorResult(fmt.Errorf("notify: %w", err))
	}
	return textResult("notification sent")
}

// ---------------------------------------------------------------------------
// clipboard_read (approval-gated) / clipboard_write
// ---------------------------------------------------------------------------

type ClipboardReadTool struct{}

func NewClipboardReadTool() ClipboardReadTool { return ClipboardReadTool{} }

func (t ClipboardReadTool) Name() string { return "clipboard_read" }

func (t ClipboardReadTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "clipboard_read",
		Description: "Read the system clipboard as text. Requires approval: the clipboard may contain passwords or other sensitive data.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t ClipboardReadTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	return agent.ApprovalRequest{
		ToolName:  "clipboard_read",
		Summary:   "read the system clipboard",
		Details:   "The clipboard content will be visible to the agent. It may contain passwords or other sensitive data.",
		Arguments: raw,
	}, nil
}

func (t ClipboardReadTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	text, err := readClipboard(ctx)
	if err != nil {
		return errorResult(fmt.Errorf("clipboard_read: %w", err))
	}
	return textResult(truncateRunes(text, maxComputerOutputRunes))
}

type ClipboardWriteTool struct{}

func NewClipboardWriteTool() ClipboardWriteTool { return ClipboardWriteTool{} }

func (t ClipboardWriteTool) Name() string { return "clipboard_write" }

func (t ClipboardWriteTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "clipboard_write",
		Description: "Write text to the system clipboard, replacing its current content. Requires approval: the current clipboard content is discarded.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}
}

func (t ClipboardWriteTool) ApprovalRequest(raw json.RawMessage) (agent.ApprovalRequest, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return agent.ApprovalRequest{}, fmt.Errorf("invalid clipboard_write arguments: %w", err)
	}
	return agent.ApprovalRequest{
		ToolName:  "clipboard_write",
		Summary:   "write to the system clipboard",
		Details:   fmt.Sprintf("The clipboard's current content will be replaced with:\n%s", truncateRunes(input.Text, 200)),
		Arguments: raw,
	}, nil
}

func (t ClipboardWriteTool) Execute(ctx context.Context, raw json.RawMessage) ToolResult {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errorResult(fmt.Errorf("invalid clipboard_write arguments: %w", err))
	}
	if len([]rune(input.Text)) > 100000 {
		return errorResult(fmt.Errorf("text is too long (max 100000 characters)"))
	}
	ctx, cancel := boundedComputerCtx(ctx)
	defer cancel()
	if err := writeClipboard(ctx, input.Text); err != nil {
		return errorResult(fmt.Errorf("clipboard_write: %w", err))
	}
	return textResult("clipboard updated")
}
