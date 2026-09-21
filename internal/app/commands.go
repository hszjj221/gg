package app

import (
	"fmt"
	"strings"
)

func parseModelCommand(prompt string) (arg string, ok bool, err error) {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 || fields[0] != "/model" {
		return "", false, nil
	}
	if len(fields) > 2 {
		return "", true, fmt.Errorf("usage: /model [provider:model]")
	}
	if len(fields) == 1 {
		return "", true, nil
	}
	return fields[1], true, nil
}

func parseNoArgCommand(prompt, command string) (bool, error) {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 || fields[0] != command {
		return false, nil
	}
	if len(fields) > 1 {
		return true, fmt.Errorf("usage: %s", command)
	}
	return true, nil
}

func parseMemoryCommand(prompt string) (command, arg string, ok bool, err error) {
	trimmed := strings.TrimSpace(prompt)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 || fields[0] != "/memory" {
		return "", "", false, nil
	}
	if len(fields) == 1 {
		return "", "", true, nil
	}
	command = fields[1]
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	afterCommand := strings.TrimSpace(strings.TrimPrefix(rest, command))
	switch command {
	case "add":
		arg = afterCommand
		if arg == "" {
			return "", "", true, fmt.Errorf("usage: /memory add <text>")
		}
		return command, arg, true, nil
	case "show":
		if len(fields) != 2 {
			return "", "", true, fmt.Errorf("usage: /memory show")
		}
		return command, "", true, nil
	default:
		return command, afterCommand, true, nil
	}
}

func parseSkillCommand(prompt string) (name, task string, ok bool) {
	trimmed := strings.TrimSpace(prompt)
	fields := strings.Fields(trimmed)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/skill:") {
		return "", "", false
	}
	name = strings.TrimPrefix(fields[0], "/skill:")
	return name, strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0])), true
}
