package cli

import "testing"

func TestParseArgsSupportsPrintModeProviderOptionsAndPrompt(t *testing.T) {
	args, err := Parse([]string{"-p", "--model", "gpt-test", "--base-url", "https://example/v1", "--api-key", "key", "--no-session", "hello"})
	if err != nil {
		t.Fatal(err)
	}

	if !args.Print || args.Model != "gpt-test" || args.BaseURL != "https://example/v1" || args.APIKey != "key" || !args.NoSession {
		t.Fatalf("unexpected args: %+v", args)
	}
	if args.Prompt != "hello" {
		t.Fatalf("unexpected prompt: %q", args.Prompt)
	}
}

func TestParseArgsRejectsUnknownFlags(t *testing.T) {
	_, err := Parse([]string{"--wat"})
	if err == nil {
		t.Fatalf("expected unknown flag error")
	}
}

func TestParseSessionsListCommand(t *testing.T) {
	args, err := Parse([]string{"--session-dir", "/tmp/sessions", "sessions", "list"})
	if err != nil {
		t.Fatal(err)
	}

	if args.Command != CommandSessionsList || args.SessionDir != "/tmp/sessions" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseResumeCommandTargetAndPrompt(t *testing.T) {
	args, err := Parse([]string{"resume", "abc123", "continue", "work"})
	if err != nil {
		t.Fatal(err)
	}

	if args.Command != CommandResume || args.ResumeTarget != "abc123" || args.Prompt != "continue work" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseContinueAndLastFlags(t *testing.T) {
	args, err := Parse([]string{"--continue", "--last"})
	if err != nil {
		t.Fatal(err)
	}

	if !args.Continue || !args.Last {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseUsageFlag(t *testing.T) {
	args, err := Parse([]string{"--usage", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}

	if !args.Usage || !args.Print || args.Prompt != "hello" {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestParseNoSkillsFlag(t *testing.T) {
	args, err := Parse([]string{"--no-skills", "-p", "hello"})
	if err != nil {
		t.Fatal(err)
	}

	if !args.NoSkills || !args.Print || args.Prompt != "hello" {
		t.Fatalf("unexpected args: %+v", args)
	}
}
