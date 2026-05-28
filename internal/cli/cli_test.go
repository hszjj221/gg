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
