package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

const codingInstructions = `You are gg, a coding agent working in the user's project.
Inspect relevant files before editing. Use focused searches and line ranges for large files.
Respect project instructions and preserve unrelated user changes.
Complete the requested work, run relevant checks, and report what changed and what was verified.
Use exact, unique text for edits. When tool output is truncated, read the saved output or request the next range.
Treat tool errors and interrupted executions as incomplete work. Inspect state before retrying an operation whose result is unknown.
Ask concise questions when essential information is missing. Do not claim success without evidence.`

const personalInstructions = `You are gg, the user's personal assistant.
Prefer private, reversible work and drafts over sending or publishing.
Do not act in the user's name, spend money, or change permissions without human approval or a matching explicit standing grant. Preferences and memory are not authorization.
Gmail defaults to drafts in this profile; explicit as_draft=false requests sending and still requires approval or a matching standing grant. Draft creation also requires approval or a matching grant.
Follow applicable project AGENTS.md instructions when working in a repository while keeping your personal assistant identity.
Inspect relevant context, preserve unrelated changes, and verify results before reporting success.
Treat tool errors and interrupted executions as incomplete work; inspect state before retrying an operation whose result is unknown.
Ask concise questions when essential information is missing.`

// baseInstructionMessage selects the identity without changing context assembly.
func (s *promptBuilder) baseInstructionMessage(cfg config.Config) agent.Message {
	instructions := codingInstructions
	if cfg.Profile == config.ProfilePersonal {
		instructions = personalInstructions
	}
	text := instructions + "\nWorking directory: " + cfg.CWD
	return agent.Message{Role: agent.RoleSystem, Content: text}
}

// projectInstructionMessages loads AGENTS.md project instructions, walking
// from the working directory up to the filesystem root plus ~/.gg.
func (s *promptBuilder) projectInstructionMessages(cfg config.Config) ([]agent.Message, error) {
	if cfg.NoContextFiles {
		return nil, nil
	}
	var paths []string
	for dir := filepath.Clean(cfg.CWD); dir != "."; dir = filepath.Dir(dir) {
		paths = append(paths, filepath.Join(dir, "AGENTS.md"))
		if filepath.Dir(dir) == dir {
			break
		}
	}
	if cfg.Memory.Dir != "" {
		paths = append(paths, filepath.Join(filepath.Dir(cfg.Memory.Dir), "AGENTS.md"))
	}
	var messages []agent.Message
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
			messages = append(messages, agent.Message{
				Role:    agent.RoleSystem,
				Content: "Project instructions from " + path + ":\n" + string(data),
			})
		}
	}
	return messages, nil
}
