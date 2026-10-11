package tools

import (
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
}

// ClassifyCommand labels command relative to cwd.
//
// Read-only commands (ls, cat, grep, git status/diff/log, and a short
// allowlist) can run without a prompt. Dangerous patterns — rm -rf of /
// or home, a download piped into a shell, sudo, disk and format tools,
// and writes outside cwd — always need an interactive approval and are
// denied under --yolo when nobody can answer. Everything else keeps the
// normal approval policy.
func ClassifyCommand(command, cwd string) CommandClassification {
	return classifyCommand(command, cwd, 0)
}

func classifyCommand(command, cwd string, depth int) CommandClassification {
	command = strings.TrimSpace(command)
	if command == "" || depth > 8 {
		return CommandClassification{}
	}
	segs, ok := splitSegments(command)
	if !ok {
		if inner := classifySubstitutions(command, cwd, depth); inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
		return CommandClassification{}
	}

	allReadOnly := true
	prev := ""
	for _, seg := range segs {
		if strings.TrimSpace(seg.text) == "" {
			continue
		}
		class := classifySegment(seg.text, cwd, depth)
		if class.Risk == agent.ApprovalRiskDangerous {
			return class
		}
		name := commandBase(seg.text)
		if seg.sep == "|" && isShell(name) && isDownloader(prev) {
			return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: "piped download into a shell"}
		}
		if isDownloader(name) {
			prev = name
		} else if name != "" {
			prev = name
		}
		if class.Risk != agent.ApprovalRiskReadOnly {
			allReadOnly = false
		}
	}
	if inner := classifySubstitutions(command, cwd, depth); inner.Risk == agent.ApprovalRiskDangerous {
		return inner
	} else if inner.Risk != agent.ApprovalRiskReadOnly && hasSubstitution(command) {
		allReadOnly = false
	}
	if allReadOnly {
		return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
	}
	return CommandClassification{}
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
// A false ok means the line is too messy to treat as read-only.
func splitSegments(command string) ([]shellSegment, bool) {
	var segs []shellSegment
	var b strings.Builder
	quote := byte(0)
	escaped := false
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
	if quote != 0 {
		return nil, false
	}
	flush("")
	return segs, true
}

func classifySegment(segment, cwd string, depth int) CommandClassification {
	words, redirects, ok := parseSimpleCommand(segment)
	if !ok {
		return CommandClassification{}
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
	name := filepath.Base(words[0])
	args := words[1:]
	if name == "env" {
		rest, ok := stripEnvFlags(args)
		if !ok {
			return CommandClassification{}
		}
		if len(rest) == 0 {
			return CommandClassification{Risk: agent.ApprovalRiskReadOnly, Reason: "read-only command"}
		}
		name = filepath.Base(rest[0])
		args = rest[1:]
	}
	if reason := dangerousArgv(name, args, cwd); reason != "" {
		return CommandClassification{Risk: agent.ApprovalRiskDangerous, Reason: reason}
	}
	if isShell(name) {
		if script, ok := shellDashC(args); ok {
			inner := classifyCommand(script, cwd, depth+1)
			if inner.Risk == agent.ApprovalRiskDangerous {
				return inner
			}
		}
		return CommandClassification{}
	}
	if name == "eval" && len(args) > 0 {
		inner := classifyCommand(strings.Join(args, " "), cwd, depth+1)
		if inner.Risk == agent.ApprovalRiskDangerous {
			return inner
		}
		return CommandClassification{}
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
	}
	if isDiskCommand(name) {
		return "disk or format command: " + name
	}
	return ""
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
	case "ls", "cat", "head", "tail", "wc", "pwd", "whoami", "id", "date",
		"uname", "file", "stat", "du", "df", "echo", "printf", "true", "false",
		"which", "basename", "dirname", "realpath", "readlink", "hostname",
		"printenv", "grep", "egrep", "fgrep":
		return true
	default:
		return false
	}
}

func gitReadOnly(args []string) bool {
	i := 0
	for i < len(args) {
		a := args[i]
		switch a {
		case "-C", "--git-dir", "--work-tree", "-c":
			if i+1 >= len(args) {
				return false
			}
			i += 2
			continue
		case "--no-pager", "--paginate", "--bare", "-P":
			i++
			continue
		default:
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
		return true
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
	cwdClean := filepath.Clean(cwd)
	var abs string
	if strings.HasPrefix(target, "/") {
		abs = filepath.Clean(target)
	} else {
		abs = filepath.Clean(filepath.Join(cwdClean, target))
	}
	if abs == cwdClean {
		return false
	}
	sep := string(filepath.Separator)
	// Absolute POSIX targets keep slash even when the host separator differs.
	if strings.HasPrefix(abs, cwdClean+"/") || strings.HasPrefix(abs, cwdClean+sep) {
		return false
	}
	rel, err := filepath.Rel(cwdClean, abs)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+sep) || strings.HasPrefix(rel, "../")
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
	words = stripAssignments(words)
	if len(words) == 0 {
		return ""
	}
	name := filepath.Base(words[0])
	if name == "sudo" || name == "doas" || name == "command" {
		rest := words[1:]
		for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
			rest = rest[1:]
		}
		if len(rest) == 0 {
			return name
		}
		return filepath.Base(rest[0])
	}
	return name
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
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			j := strings.IndexByte(s[i+1:], '`')
			if j < 0 {
				return out
			}
			out = append(out, s[i+1:i+1+j])
			i += 1 + j
			continue
		}
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '(' {
			depth := 1
			j := i + 2
			for j < len(s) && depth > 0 {
				if s[j] == '(' {
					depth++
				} else if s[j] == ')' {
					depth--
				}
				j++
			}
			if depth != 0 {
				return out
			}
			out = append(out, s[i+2:j-1])
			i = j - 1
		}
	}
	return out
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
