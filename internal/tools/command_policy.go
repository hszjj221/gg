package tools

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/hszjj221/gg/internal/agent"
)

// CommandClassification is the lightweight risk label for one shell command.
// It is not a full shell parser. See the security model in docs/architecture.md
// for what it catches and what it deliberately does not.
type CommandClassification struct {
	// Risk is "", agent.ApprovalRiskReadOnly, or agent.ApprovalRiskDangerous.
	Risk string
	// Reason is shown on the approval request. Empty when Risk is empty.
	Reason string
	// Hold means the lists did not grant a run, and a model must not grant
	// one either: the line is unparseable, an opaque shell, or some other
	// shape the classifier will not treat as safe. Risk stays empty so the
	// ordinary approval policy is unchanged when the model check is off.
	Hold bool
}

// ClassifyCommand labels command relative to cwd.
//
// Read-only commands (ls, cat, grep, git status/diff/log, and a short
// allowlist) can run without a prompt. Dangerous patterns — rm -rf of /
// or home, anything piped into a shell, sudo, disk and format tools,
// eval, an interpreter -c/-e one-liner, command substitution in command
// position, and writes outside cwd — always need an interactive approval
// and are denied under --yolo when nobody can answer. Everything else
// keeps the normal approval policy. Hold is set when the line is not a
// list hit but must not be cleared by a model; Risk stays empty then.
func ClassifyCommand(command, cwd string) CommandClassification {
	return classifyCommand(command, cwd, 0)
}

func classifyCommand(command, cwd string, depth int) CommandClassification {
	command = strings.TrimSpace(command)
	if command == "" {
		return CommandClassification{}
	}
	if depth > 8 {
		return CommandClassification{Hold: true}
	}
	if commandPositionSubst(command) {
		if inner := classifySubstitutions(command, cwd, depth); inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: "command substitution executes its output"}
	}
	segs, ok := splitSegments(command)
	if !ok {
		if inner := classifySubstitutions(command, cwd, depth); inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
		return CommandClassification{Hold: true}
	}

	allReadOnly := true
	held := false
	var pipe pipeInfo
	for _, seg := range segs {
		text := strings.TrimSpace(seg.text)
		if text == "" {
			continue
		}
		if stripped, ok := stripBackground(text); ok {
			text = stripped
		}
		if text == "" {
			continue
		}
		if seg.sep != "|" {
			pipe = pipeInfo{}
		}
		name := commandBase(text)
		if seg.sep == "|" && isShell(name) {
			if pipe.downloader {
				return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: "piped download into a shell"}
			}
			return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: "piped into a shell"}
		}
		class := classifySegment(text, cwd, depth)
		if class.Risk == agent.ApprovalRiskDangerous {
			return class
		}
		if class.Hold {
			held = true
		}
		if isDownloader(name) {
			pipe.downloader = true
		}
		if class.Risk != agent.ApprovalRiskReadOnly {
			allReadOnly = false
		}
	}
	if inner := classifySubstitutions(command, cwd, depth); inner.Risk == agent.ApprovalRiskDangerous {
		return inner
	} else if inner.Risk != agent.ApprovalRiskReadOnly && hasSubstitution(command) {
		allReadOnly = false
		held = true
	}
	if held {
		return CommandClassification{Hold: true}
	}
	if allReadOnly {
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
	}
	return CommandClassification{}
}

// pipeInfo tracks the current pipeline. &&, ||, and ; start a new one.
type pipeInfo struct {
	downloader bool
}

func classifySubstitutions(command, cwd string, depth int) CommandClassification {
	saw := false
	for _, sub := range extractSubstitutions(command) {
		saw = true
		inner := classifyCommand(sub, cwd, depth+1)
		if inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
		if inner.Risk != agent.ApprovalRiskReadOnly {
			return CommandClassification{}
		}
	}
	if !saw {
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly}
	}
	return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
}

func hasSubstitution(command string) bool {
	return len(extractSubstitutions(command)) > 0
}

type shellSegment struct {
	text string
	sep  string
}

// splitSegments splits on |, ||, &&, ; and newlines outside quotes.
// Parentheses keep their interior together so `(echo | sh)` stays one
// subshell. A false ok means the line is too messy to treat as read-only.
func splitSegments(command string) ([]shellSegment, bool) {
	var segs []shellSegment
	var b strings.Builder
	quote := byte(0)
	escaped := false
	depth := 0
	sep := ""
	flush := func(next string) {
		segs = append(segs, shellSegment{text: strings.TrimSpace(b.String()), sep: sep})
		b.Reset()
		sep = next
	}
	for i := 0; i < len(command); {
		c := command[i]
		if quote != 0 {
			b.WriteByte(c)
			if escaped {
				escaped = false
				i++
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		if escaped {
			b.WriteByte(c)
			escaped = false
			i++
			continue
		}
		if c == '\\' {
			b.WriteByte(c)
			escaped = true
			i++
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(c)
			i++
			continue
		}
		if c == '(' {
			depth++
			b.WriteByte(c)
			i++
			continue
		}
		if c == ')' {
			if depth == 0 {
				return nil, false
			}
			depth--
			b.WriteByte(c)
			i++
			continue
		}
		if depth > 0 {
			b.WriteByte(c)
			i++
			continue
		}
		if c == '|' && i+1 < len(command) && command[i+1] == '|' {
			flush("||")
			i += 2
			continue
		}
		if c == '&' && i+1 < len(command) && command[i+1] == '&' {
			flush("&&")
			i += 2
			continue
		}
		if c == '|' {
			flush("|")
			i++
			continue
		}
		if c == ';' || c == '\n' {
			flush(";")
			i++
			continue
		}
		b.WriteByte(c)
		i++
	}
	if quote != 0 || depth != 0 {
		return nil, false
	}
	flush("")
	return segs, true
}

func classifySegment(segment, cwd string, depth int) CommandClassification {
	if depth > 8 {
		return CommandClassification{Hold: true}
	}
	if body, ok := unwrapSubshell(segment); ok {
		return classifySubshell(body, cwd, depth)
	}
	words, redirects, ok := parseSimpleCommand(segment)
	if !ok {
		return CommandClassification{Hold: true}
	}
	words = stripAssignments(words)
	if len(words) == 0 {
		if reason := redirectRisk(redirects, cwd); reason != "" {
			return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: reason}
		}
		if len(redirects) > 0 {
			return CommandClassification{}
		}
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
	}
	name, args, hold := unwrapWrappers(filepath.Base(words[0]), words[1:])
	if hold {
		return CommandClassification{Hold: true}
	}
	if name == "env" && len(args) == 0 {
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
	}
	if reason := dangerousArgv(name, args, cwd); reason != "" {
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: reason}
	}
	if reason := inlineCodeReason(name, args); reason != "" {
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: reason}
	}
	if isLauncher(name) {
		return classifyLauncher(args, cwd, depth)
	}
	if isShell(name) {
		if script, ok := shellDashC(args); ok {
			inner := classifyCommand(script, cwd, depth+1)
			if inner.Risk == agent.ApprovalRiskDangerous {
				return inner
			}
			if inner.Risk == agent.ApprovalRiskReadOnly && !inner.Hold {
				return inner
			}
			if inner.Hold {
				return CommandClassification{Hold: true}
			}
			return CommandClassification{}
		}
		return CommandClassification{Hold: true}
	}
	if name == "eval" {
		if len(args) > 0 {
			inner := classifyCommand(strings.Join(args, " "), cwd, depth+1)
			if inner.Risk == agent.ApprovalRiskDangerous {
				return inner
			}
		}
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: "eval executes shell code"}
	}
	if name == "source" || name == "." {
		return CommandClassification{Hold: true}
	}
	if name == "find" && !findReadOnly(args) {
		return CommandClassification{Hold: true}
	}
	if reason := redirectRisk(redirects, cwd); reason != "" {
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: reason}
	}
	if len(redirects) > 0 {
		return CommandClassification{}
	}
	if commandReadOnly(name, args) {
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
	}
	return CommandClassification{}
}

func dangerousArgv(name string, args []string, cwd string) string {
	switch name {
	case "sudo", "doas", "pkexec":
		return "uses " + name
	case "su":
		return "uses su"
	case "shutdown", "reboot", "poweroff", "halt", "telinit":
		return "power command: " + name
	case "dd":
		for _, a := range args {
			if !strings.HasPrefix(a, "of=") {
				continue
			}
			dest := strings.TrimPrefix(a, "of=")
			if strings.HasPrefix(dest, "/dev/") && !isHarmlessDevice(dest) {
				return "dd writes to " + dest
			}
			if outsideCWD(dest, cwd) {
				return "dd writes outside the working directory (" + dest + ")"
			}
		}
		return ""
	case "rm":
		return rmRisk(args, cwd)
	case "chmod", "chown":
		return pathsOutsideRisk(name+" changes entries outside the working directory", args, cwd)
	case "cp", "mv", "install", "ln":
		return destRisk(name, args, cwd)
	case "tee":
		return pathsOutsideRisk("tee writes outside the working directory", args, cwd)
	case "touch", "mkdir", "rmdir":
		return pathsOutsideRisk(name+" writes outside the working directory", args, cwd)
	case "xargs":
		for _, a := range args {
			base := filepath.Base(a)
			if isShell(base) || base == "eval" {
				return "xargs runs a shell"
			}
		}
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		for _, a := range args {
			flag := strings.ToLower(strings.TrimLeft(a, "-"))
			if flag == "encodedcommand" || flag == "enc" || flag == "ec" {
				return "encoded powershell command"
			}
		}
	}
	if isDiskCommand(name) {
		return "disk or format command: " + name
	}
	return ""
}

// stripBackground removes trailing unquoted & markers so `rm -rf / &`
// is still the rm command. The returned ok is false when there is no
// trailing background marker.
func stripBackground(s string) (string, bool) {
	cur := s
	changed := false
	for {
		next, ok := stripOneBackground(cur)
		if !ok {
			break
		}
		cur = next
		changed = true
	}
	if !changed {
		return s, false
	}
	return strings.TrimSpace(cur), true
}

func stripOneBackground(s string) (string, bool) {
	quote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '&' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '&' {
			return "", false
		}
		if strings.TrimSpace(s[i+1:]) != "" {
			return "", false
		}
		return strings.TrimSpace(s[:i]), true
	}
	return "", false
}

func isLauncher(name string) bool {
	switch name {
	case "nice", "nohup", "time", "setsid", "timeout", "ionice", "stdbuf", "flock", "watch", "xargs":
		return true
	default:
		return false
	}
}

// classifyLauncher scans every non-flag suffix of a wrapper. A dangerous
// suffix is a denylist hit (`nice rm -rf /`, `xargs rm -rf /`). Anything
// else is held so a model cannot clear the wrapper on a low score.
func classifyLauncher(args []string, cwd string, depth int) CommandClassification {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			continue
		}
		inner := classifySegment(strings.Join(args[i:], " "), cwd, depth+1)
		if inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
	}
	return CommandClassification{Hold: true}
}

func inlineCodeReason(name string, args []string) string {
	switch name {
	case "python", "python2", "python3", "perl", "ruby", "node", "nodejs", "php", "lua", "osascript":
	default:
		return ""
	}
	for _, a := range args {
		switch a {
		case "-c", "-e", "-r", "-p", "--eval", "--print":
			return "interpreter executes inline code (" + name + " " + a + ")"
		}
	}
	return ""
}

// classifySubshell re-enters on a parenthesized body. A dangerous body
// stays dangerous. A fully read-only body stays read-only. Anything else
// keeps the ordinary policy (Hold included) so `(go test)` is judged like
// `go test`.
func classifySubshell(body, cwd string, depth int) CommandClassification {
	inner := classifyCommand(body, cwd, depth+1)
	if inner.Risk == agent.ApprovalRiskDangerous || inner.Hold || inner.Risk == agent.ApprovalRiskReadOnly {
		return inner
	}
	return CommandClassification{}
}

// unwrapSubshell returns the body of a segment that is entirely one
// parenthesized subshell. $(...) is command substitution, not a subshell
// grouping, and is left alone.
func unwrapSubshell(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '(' || strings.HasPrefix(s, "$(") {
		return "", false
	}
	depth := 0
	quote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch c {
		case '\\':
			escaped = true
		case '\'', '"':
			quote = c
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", false
			}
			if depth == 0 {
				if strings.TrimSpace(s[i+1:]) != "" {
					return "", false
				}
				return strings.TrimSpace(s[1:i]), true
			}
		}
	}
	return "", false
}

// commandPositionSubst reports whether any segment's command word is a
// substitution, so the substitution's output is executed as code.
func commandPositionSubst(command string) bool {
	segs, ok := splitSegments(command)
	if !ok {
		return leadingSubst(command)
	}
	for _, seg := range segs {
		if leadingSubst(seg.text) {
			return true
		}
	}
	return false
}

func leadingSubst(s string) bool {
	for _, field := range strings.Fields(s) {
		if isAssignment(field) {
			continue
		}
		return strings.HasPrefix(field, "$(") || strings.HasPrefix(field, "`")
	}
	return false
}

// unwrapWrappers drops env, command, exec, and busybox shell applets so
// the program that actually runs is what the lists see. hold is true when
// a wrapper's flags are not understood.
func unwrapWrappers(name string, args []string) (string, []string, bool) {
	for range 8 {
		switch name {
		case "env":
			rest, ok := stripEnvFlags(args)
			if !ok {
				return "", nil, true
			}
			if len(rest) == 0 {
				return "env", nil, false
			}
			name = filepath.Base(rest[0])
			args = rest[1:]
		case "command":
			rest, ok := stripCommandFlags(args)
			if !ok {
				return "", nil, true
			}
			if len(rest) == 0 {
				return "command", nil, false
			}
			name = filepath.Base(rest[0])
			args = rest[1:]
		case "exec":
			if len(args) == 0 {
				return "exec", nil, false
			}
			name = filepath.Base(args[0])
			args = args[1:]
		case "busybox":
			if len(args) == 0 {
				return name, nil, false
			}
			applet := filepath.Base(args[0])
			if isShell(applet) || applet == "eval" || applet == "exec" {
				name = applet
				args = args[1:]
				continue
			}
			return name, args, false
		default:
			return name, args, false
		}
	}
	return "", nil, true
}

func stripCommandFlags(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-p", "-v", "-V":
			continue
		case "--":
			return args[i+1:], true
		default:
			if strings.HasPrefix(a, "-") {
				return nil, false
			}
			return args[i:], true
		}
	}
	return nil, true
}

func commandReadOnly(name string, args []string) bool {
	switch name {
	case "git":
		return gitReadOnly(args)
	case "go":
		return goReadOnly(args)
	case "find":
		return findReadOnly(args)
	case "rg":
		return rgReadOnly(args)
	case "date":
		return dateReadOnly(args)
	case "ls", "cat", "head", "tail", "wc", "pwd", "whoami", "id",
		"uname", "file", "stat", "du", "df", "echo", "printf", "true", "false",
		"which", "basename", "dirname", "realpath", "readlink", "hostname",
		"printenv", "grep", "egrep", "fgrep":
		return true
	default:
		return false
	}
}

// dateReadOnly allows formatting and parsing. Setting the clock
// (date -s, date --set, or a positional timestamp) stays approval-gated.
func dateReadOnly(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			for _, rest := range args[i+1:] {
				if !strings.HasPrefix(rest, "+") {
					return false
				}
			}
			return true
		}
		if a == "-s" || a == "--set" || strings.HasPrefix(a, "--set=") {
			return false
		}
		if a == "--date" {
			if i+1 >= len(args) {
				return false
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--date=") || strings.HasPrefix(a, "--") {
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags := a[1:]
			if strings.Contains(flags, "s") {
				return false
			}
			if !consumeDateArg(flags, args, &i, "d") || !consumeDateArg(flags, args, &i, "r") || !consumeDateArg(flags, args, &i, "f") {
				return false
			}
			continue
		}
		if strings.HasPrefix(a, "+") {
			continue
		}
		return false
	}
	return true
}

// consumeDateArg skips the argument of a short flag when that flag is present.
// An attached argument (-dSTRING) needs nothing further. A missing argument fails closed.
func consumeDateArg(flags string, args []string, i *int, flag string) bool {
	idx := strings.Index(flags, flag)
	if idx < 0 {
		return true
	}
	if idx < len(flags)-1 {
		return true
	}
	if *i+1 >= len(args) {
		return false
	}
	*i++
	return true
}

func gitReadOnly(args []string) bool {
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-C", "--git-dir", "--work-tree":
			if i+1 >= len(args) {
				return false
			}
			i += 2
			continue
		case "-c", "--config-env":
			// core.fsmonitor and other config values can be hook commands.
			// -c after the subcommand is a diff option and is handled below.
			return false
		case "--no-pager", "--paginate", "--bare", "-P":
			i++
			continue
		default:
			if strings.HasPrefix(a, "--config-env=") {
				return false
			}
			if strings.HasPrefix(a, "-") {
				return false
			}
		}
		break
	}
	if i >= len(args) {
		return false
	}
	switch args[i] {
	case "status", "diff", "log", "show":
		for _, a := range args[i+1:] {
			if a == "--output" || a == "-o" || strings.HasPrefix(a, "--output=") {
				return false
			}
			// These diff options run an external program.
			if a == "--ext-diff" || a == "--textconv" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func goReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version":
		return true
	case "list":
		return goListReadOnly(args[1:])
	case "env":
		for _, a := range args[1:] {
			if a == "-w" || a == "-u" || strings.Contains(a, "=") {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// goListReadOnly is true only for an explicit non-mutating module mode.
// go list -mod=mod may download modules and rewrite go.mod and go.sum.
// The last -mod value wins, matching the go command.
func goListReadOnly(args []string) bool {
	mod := ""
	saw := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if a == "-mod" {
			if i+1 >= len(args) {
				return false
			}
			i++
			mod = args[i]
			saw = true
			continue
		}
		if strings.HasPrefix(a, "-mod=") {
			mod = strings.TrimPrefix(a, "-mod=")
			saw = true
		}
	}
	return saw && (mod == "readonly" || mod == "vendor")
}

func findReadOnly(args []string) bool {
	for _, a := range args {
		switch a {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls", "-fprint", "-fprint0", "-fprintf":
			return false
		}
	}
	return true
}

func rgReadOnly(args []string) bool {
	for _, a := range args {
		if a == "--pre" || strings.HasPrefix(a, "--pre=") || a == "--pre-glob" {
			return false
		}
	}
	return true
}

func rmRisk(args []string, cwd string) string {
	recursive := false
	var paths []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			paths = append(paths, args[i+1:]...)
			break
		}
		if a == "--recursive" || a == "-r" || a == "-R" {
			recursive = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if strings.ContainsAny(a, "rR") {
				recursive = true
			}
			continue
		}
		paths = append(paths, a)
	}
	for _, p := range paths {
		if recursive && isCatastrophicPath(p) {
			return "rm -rf targets " + p
		}
		if outsideCWD(p, cwd) {
			if recursive {
				return "rm -rf outside the working directory (" + p + ")"
			}
			return "rm deletes outside the working directory (" + p + ")"
		}
	}
	return ""
}

func destRisk(cmd string, args []string, cwd string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if (a == "-t" || a == "--target-directory") && i+1 < len(args) {
			if outsideCWD(args[i+1], cwd) {
				return cmd + " writes outside the working directory (" + args[i+1] + ")"
			}
		}
		if strings.HasPrefix(a, "--target-directory=") {
			dest := strings.TrimPrefix(a, "--target-directory=")
			if outsideCWD(dest, cwd) {
				return cmd + " writes outside the working directory (" + dest + ")"
			}
		}
	}
	dest := lastNonFlag(args)
	if dest != "" && outsideCWD(dest, cwd) {
		return cmd + " writes outside the working directory (" + dest + ")"
	}
	return ""
}

func pathsOutsideRisk(prefix string, args []string, cwd string) string {
	for _, a := range args {
		if a == "--" || strings.HasPrefix(a, "-") {
			continue
		}
		if outsideCWD(a, cwd) {
			return prefix + " (" + a + ")"
		}
	}
	return ""
}

func redirectRisk(targets []string, cwd string) string {
	for _, target := range targets {
		if outsideCWD(target, cwd) {
			return "redirect writes outside the working directory (" + target + ")"
		}
	}
	return ""
}

func lastNonFlag(args []string) string {
	last := ""
	for _, a := range args {
		if a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		last = a
	}
	return last
}

func isCatastrophicPath(p string) bool {
	p = strings.TrimSpace(p)
	trimmed := strings.TrimRight(p, "/")
	if trimmed == "" && strings.HasPrefix(p, "/") {
		trimmed = "/"
	}
	switch trimmed {
	case "/", "~", "$HOME", "${HOME}", "/home", "/Users", "/root":
		return true
	}
	switch p {
	case "/*", "/.", "/..", "~/*", "$HOME/*", "${HOME}/*", "/home/*", "/Users/*", "/root/*":
		return true
	}
	return false
}

func outsideCWD(target, cwd string) bool {
	target = strings.TrimSpace(target)
	if target == "" || isHarmlessDevice(target) {
		return false
	}
	if isCatastrophicPath(target) {
		return true
	}
	if strings.HasPrefix(target, "~") || strings.HasPrefix(target, "$HOME") || strings.HasPrefix(target, "${HOME}") {
		return true
	}
	resolvedTarget, ok := resolveCommandPath(cwd, target)
	if !ok {
		return true
	}
	resolvedCWD, ok := resolveCommandPath("/", cwd)
	if !ok {
		return true
	}
	return !pathInside(resolvedCWD, resolvedTarget)
}

// resolveCommandPath joins target onto base and walks each component.
// ".." is applied only after earlier symlinks are resolved, and a missing
// suffix stays on that resolved parent. A path that cannot be resolved
// fails closed.
func resolveCommandPath(base, target string) (string, bool) {
	if !strings.HasPrefix(target, "/") {
		if strings.HasPrefix(base, "/") {
			base = strings.TrimRight(base, "/")
			if base == "" {
				base = "/"
			}
			target = base + "/" + target
		} else {
			target = filepath.Join(base, target)
		}
	}
	return walkCommandPath(target)
}

func walkCommandPath(p string) (string, bool) {
	parts := strings.Split(p, "/")
	resolved := "/"
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if resolved != "/" {
				resolved = filepath.Dir(resolved)
			}
			continue
		}
		next := "/" + part
		if resolved != "/" {
			next = resolved + "/" + part
		}
		info, err := os.Lstat(next)
		if err != nil {
			if !os.IsNotExist(err) {
				return "", false
			}
			for _, r := range parts[i:] {
				if r == "" || r == "." {
					continue
				}
				if r == ".." {
					if resolved != "/" {
						resolved = filepath.Dir(resolved)
					}
					continue
				}
				if resolved == "/" {
					resolved = "/" + r
				} else {
					resolved = resolved + "/" + r
				}
			}
			return resolved, true
		}
		if info.Mode()&os.ModeSymlink != 0 {
			dest, err := filepath.EvalSymlinks(next)
			if err != nil || !strings.HasPrefix(dest, "/") {
				return "", false
			}
			resolved = filepath.Clean(dest)
			continue
		}
		resolved = next
	}
	return resolved, true
}

func pathInside(parent, child string) bool {
	if parent == "/" {
		return strings.HasPrefix(child, "/")
	}
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	return strings.HasPrefix(child, parent+"/") || strings.HasPrefix(child, parent+sep)
}

func isHarmlessDevice(p string) bool {
	switch p {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/zero":
		return true
	}
	return strings.HasPrefix(p, "/dev/fd/")
}

func isDiskCommand(name string) bool {
	switch name {
	case "fdisk", "cfdisk", "sfdisk", "parted", "gdisk", "sgdisk",
		"diskutil", "wipefs", "shred", "mkfs", "format", "newfs":
		return true
	}
	return strings.HasPrefix(name, "mkfs.") || strings.HasPrefix(name, "newfs_")
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "csh", "tcsh", "ash":
		return true
	}
	return false
}

func isDownloader(name string) bool {
	switch name {
	case "curl", "wget", "fetch":
		return true
	}
	return false
}

func shellDashC(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" || args[i] == "-lc" {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
	}
	return "", false
}

func commandBase(segment string) string {
	words, _, ok := parseSimpleCommand(segment)
	if !ok {
		return ""
	}
	return commandName(stripAssignments(words))
}

// commandName is the program a segment runs. sudo, doas, command, and env
// are unwrapped so `curl … | env sh` is still a pipe into a shell.
func commandName(words []string) string {
	if len(words) == 0 {
		return ""
	}
	name := filepath.Base(words[0])
	switch name {
	case "sudo", "doas", "command", "exec":
		rest := words[1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			if rest[0] == "--" {
				rest = rest[1:]
				break
			}
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return name
		}
		return commandName(rest)
	case "env":
		rest, ok := stripEnvFlags(words[1:])
		if !ok || len(rest) == 0 {
			return name
		}
		return commandName(rest)
	case "busybox":
		if len(words) > 1 {
			applet := filepath.Base(words[1])
			if isShell(applet) || applet == "eval" || applet == "exec" {
				return commandName(words[1:])
			}
		}
		return name
	default:
		return name
	}
}

func stripAssignments(words []string) []string {
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	return words[i:]
}

func isAssignment(word string) bool {
	if word == "" || strings.HasPrefix(word, "=") {
		return false
	}
	eq := strings.IndexByte(word, '=')
	if eq <= 0 {
		return false
	}
	name := word[:eq]
	for i, r := range name {
		if i == 0 && !unicode.IsLetter(r) && r != '_' {
			return false
		}
		if i > 0 && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// stripEnvFlags drops env's own flags and NAME=value assignments.
// ok is false when the flags are not understood. A nil rest with ok means
// env itself prints the environment and runs no command.
func stripEnvFlags(args []string) ([]string, bool) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-i", "-0", "-v":
			i++
			continue
		case "-u":
			if i+1 >= len(args) {
				return nil, false
			}
			i += 2
			continue
		case "--":
			return args[i+1:], true
		default:
			if strings.HasPrefix(a, "-") {
				return nil, false
			}
			if isAssignment(a) {
				i++
				continue
			}
			return args[i:], true
		}
	}
	return nil, true
}

func extractSubstitutions(s string) []string {
	var out []string
	quote := byte(0)
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			// Single quotes suppress expansion. Double quotes do not.
			if quote != '"' {
				continue
			}
		} else {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '\'' || c == '"' {
				quote = c
				continue
			}
		}
		if c == '`' {
			body, end, ok := scanBacktick(s, i)
			if !ok {
				return out
			}
			out = append(out, body)
			i = end
			continue
		}
		if c == '$' && i+1 < len(s) && s[i+1] == '(' {
			body, end, ok := scanDollarParen(s, i)
			if !ok {
				return out
			}
			out = append(out, body)
			i = end
		}
	}
	return out
}

func scanBacktick(s string, start int) (body string, end int, ok bool) {
	escaped := false
	for j := start + 1; j < len(s); j++ {
		if escaped {
			escaped = false
			continue
		}
		if s[j] == '\\' {
			escaped = true
			continue
		}
		if s[j] == '`' {
			return s[start+1 : j], j, true
		}
	}
	return "", 0, false
}

// scanDollarParen reads a $(...) body. Quotes and escapes inside the
// substitution are tracked so a quoted ")" does not close it early.
func scanDollarParen(s string, dollar int) (body string, end int, ok bool) {
	quote := byte(0)
	escaped := false
	depth := 1
	for j := dollar + 2; j < len(s); j++ {
		c := s[j]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			depth--
			if depth == 0 {
				return s[dollar+2 : j], j, true
			}
		}
	}
	return "", 0, false
}

// parseSimpleCommand tokenizes one pipeline segment. redirects are output
// targets only; fd dups such as 2>&1 are ignored. ok is false for heredocs,
// background jobs, and unbalanced quotes — those are not auto-allowed.
func parseSimpleCommand(s string) (words []string, redirects []string, ok bool) {
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words = append(words, word.String())
			word.Reset()
		}
	}
	quote := byte(0)
	escaped := false
	for i := 0; i < len(s); {
		c := s[i]
		if quote != 0 {
			if escaped {
				word.WriteByte(c)
				escaped = false
				i++
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				i++
				continue
			}
			if c == quote {
				quote = 0
				i++
				continue
			}
			word.WriteByte(c)
			i++
			continue
		}
		if escaped {
			word.WriteByte(c)
			escaped = false
			i++
			continue
		}
		switch c {
		case '\\':
			escaped = true
			i++
		case '\'', '"':
			quote = c
			i++
		case ' ', '\t', '\r':
			flush()
			i++
		case '>':
			pending := word.String()
			if pending == "&" || isDigits(pending) {
				word.Reset()
			} else {
				flush()
			}
			target, next, dup, good := readRedirectTarget(s, i)
			if !good {
				return nil, nil, false
			}
			if !dup {
				redirects = append(redirects, target)
			}
			i = next
		case '<':
			pending := word.String()
			if isDigits(pending) {
				word.Reset()
			} else {
				flush()
			}
			next, good := consumeInputRedirect(s, i)
			if !good {
				return nil, nil, false
			}
			i = next
		case '&':
			if i+1 < len(s) && s[i+1] == '>' {
				word.WriteByte('&')
				i++
				continue
			}
			return nil, nil, false
		case '|', ';', '\n':
			return nil, nil, false
		default:
			word.WriteByte(c)
			i++
		}
	}
	if quote != 0 || escaped {
		return nil, nil, false
	}
	flush()
	return words, redirects, true
}

func readRedirectTarget(s string, i int) (target string, next int, dup bool, ok bool) {
	if i >= len(s) || s[i] != '>' {
		return "", i, false, false
	}
	i++
	if i < len(s) && s[i] == '>' {
		i++
	}
	if i < len(s) && s[i] == '|' {
		i++
	}
	if i < len(s) && s[i] == '&' {
		i++
		if i < len(s) && (s[i] == '-' || unicode.IsDigit(rune(s[i]))) {
			if s[i] == '-' {
				i++
			}
			for i < len(s) && unicode.IsDigit(rune(s[i])) {
				i++
			}
			return "", i, true, true
		}
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	word, next, ok := readWord(s, i)
	if !ok {
		return "", next, false, false
	}
	return word, next, false, true
}

func consumeInputRedirect(s string, i int) (int, bool) {
	if i >= len(s) || s[i] != '<' {
		return i, false
	}
	i++
	if i < len(s) && s[i] == '<' {
		return i, false // heredoc
	}
	if i < len(s) && s[i] == '&' {
		i++
		for i < len(s) && (unicode.IsDigit(rune(s[i])) || s[i] == '-') {
			i++
		}
		return i, true
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	_, n, ok := readWord(s, i)
	if !ok {
		return n, false
	}
	return n, true
}

func readWord(s string, i int) (string, int, bool) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return "", i, false
	}
	var b strings.Builder
	quote := byte(0)
	escaped := false
	for i < len(s) {
		c := s[i]
		if quote != 0 {
			if escaped {
				b.WriteByte(c)
				escaped = false
				i++
				continue
			}
			if quote == '"' && c == '\\' {
				escaped = true
				i++
				continue
			}
			if c == quote {
				quote = 0
				i++
				continue
			}
			b.WriteByte(c)
			i++
			continue
		}
		if escaped {
			b.WriteByte(c)
			escaped = false
			i++
			continue
		}
		if c == '\\' {
			escaped = true
			i++
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			i++
			continue
		}
		if strings.ContainsRune(" \t\n><|&;", rune(c)) {
			break
		}
		b.WriteByte(c)
		i++
	}
	if quote != 0 || escaped {
		return "", i, false
	}
	if b.Len() == 0 {
		return "", i, false
	}
	return b.String(), i, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
