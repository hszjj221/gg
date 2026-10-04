package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/memory"
	"github.com/hszjj221/gg/internal/skills"
	"github.com/hszjj221/gg/internal/userprofile"
)

// promptBuilder reads personal, project and skill context without mutating a
// conversation or allocating execution resources.
type promptBuilder struct {
	profile  userprofile.Profile
	memStore memory.StoreAPI
	skillSet skills.Set
}

func (s *promptBuilder) systemMessages(cfg config.Config) ([]agent.Message, error) {
	// Prompt order: stable identity first, then memory, then project
	// context, then skills.
	messages := []agent.Message{s.codingInstructionMessage(cfg)}
	if block := s.profile.PromptBlock(); block != "" {
		messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: block})
	}
	if cfg.Memory.Enabled {
		snapshot, err := s.memStore.LoadCurated(cfg.Memory.MaxPromptTokens)
		if err != nil {
			return nil, err
		}
		if prompt := s.memStore.CuratedSystemPrompt(snapshot); prompt != "" {
			messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: prompt, Timestamp: time.Now().UnixMilli()})
		}
		tail, err := s.memStore.LoadDailyTail(cfg.Memory.DailyLogTailTokens)
		if err != nil {
			return nil, err
		}
		if content := strings.TrimSpace(tail.Content); content != "" {
			messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: "Today's log:\n" + content, Timestamp: time.Now().UnixMilli()})
		}
	}
	project, err := s.projectInstructionMessages(cfg)
	if err != nil {
		return nil, err
	}
	messages = append(messages, project...)
	messages = append(messages, skillSystemMessages(s.skillSet)...)
	return messages, nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// userLocation resolves the user's timezone for calendar tools. When the
// profile timezone is set but not a valid IANA name, it returns the error
// so calendar tools refuse to run rather than silently interpreting
// wall-clock input in the wrong zone.
func userLocation(profile userprofile.Profile) (*time.Location, error) {
	if profile.Timezone != "" {
		if loc, err := time.LoadLocation(profile.Timezone); err == nil {
			return loc, nil
		}
		return time.Local, fmt.Errorf("profile timezone %q is not a valid IANA name; edit ~/.gg/USER.md to fix or unset it", profile.Timezone)
	}
	return time.Local, nil
}

func skillSystemMessages(skillSet skills.Set) []agent.Message {
	prompt := skillSet.FormatSystemPrompt()
	if prompt == "" {
		return nil
	}
	return []agent.Message{{Role: agent.RoleSystem, Content: prompt, Timestamp: time.Now().UnixMilli()}}
}

func preparePrompt(prompt string, skillSet skills.Set) (string, error) {
	name, task, ok := parseSkillCommand(prompt)
	if !ok {
		return prompt, nil
	}
	skill, found := skillSet.Find(name)
	if !found {
		return "", fmt.Errorf("skill %q not found", name)
	}
	content, err := skills.ReadSkillFile(skill)
	if err != nil {
		return "", err
	}
	return skills.FormatForcedPrompt(skill, content, task), nil
}
