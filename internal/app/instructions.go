package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
)

const codingInstructions = `You are gg, a coding agent working in the user's project.
Inspect relevant files before editing. Use focused searches and line ranges for large files.
Respect project instructions and preserve unrelated user changes.
Complete the requested work, run relevant checks, and report what changed and what was verified.
Use exact, unique text for edits. When tool output is truncated, read the saved output or request the next range.
Treat tool errors and interrupted executions as incomplete work. Inspect state before retrying an operation whose result is unknown.
Ask concise questions when essential information is missing. Do not claim success without evidence.`

func (e *turnExecutor) instructionMessages() ([]agent.Message, error) {
	text := codingInstructions + "\nWorking directory: " + e.cfg.CWD
	if !e.cfg.NoContextFiles {
		var paths []string
		for dir := filepath.Clean(e.cfg.CWD); dir != "."; dir = filepath.Dir(dir) {
			paths = append(paths, filepath.Join(dir, "AGENTS.md"))
			if filepath.Dir(dir) == dir {
				break
			}
		}
		if e.cfg.MemoryPath != "" {
			paths = append(paths, filepath.Join(filepath.Dir(e.cfg.MemoryPath), "AGENTS.md"))
		}
		seen := map[string]bool{}
		for i := len(paths) - 1; i >= 0; i-- {
			path := paths[i]
			if seen[path] {
				continue
			}
			seen[path] = true
			file, err := os.Open(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			data, err := io.ReadAll(io.LimitReader(file, 32*1024+1))
			file.Close()
			if err != nil {
				return nil, err
			}
			if len(data) > 32*1024 {
				return nil, fmt.Errorf("project instructions exceed 32 KiB: %s", path)
			}
			if strings.TrimSpace(string(data)) != "" {
				text += "\n\nProject instructions from " + path + ":\n" + string(data)
			}
		}
	}
	return []agent.Message{{Role: agent.RoleSystem, Content: text}}, nil
}
