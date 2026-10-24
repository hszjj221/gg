package cliapp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/standingauth"
)

func standingAddArgs() []string {
	return []string{"--subject", "gg on behalf of alice@example.com", "--tools", "gmail_send", "--consequences", "E,U",
		"--arguments", `{"to":"bob@example.com","as_draft":true}`, "--ttl", "1h", "--source-quote", "For one hour gg may create Gmail drafts for bob@example.com as alice@example.com."}
}

func TestStandingAuthCLI(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr strings.Builder
	opts := Options{CWD: dir, HomeDir: filepath.Join(dir, "home"), Stdout: &stdout, Stderr: &stderr}
	add := append([]string{"standing-auth", "add"}, standingAddArgs()...)
	if code := Run(context.Background(), add, opts); code != 0 {
		t.Fatalf("add exit %d: %s", code, stderr.String())
	}
	id := strings.TrimSpace(stdout.String())
	if len(id) != 32 {
		t.Fatalf("created id: %q", id)
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"standing-auth", "list"}, opts); code != 0 {
		t.Fatalf("list exit %d: %s", code, stderr.String())
	}
	for _, want := range []string{id, "alice@example.com", "gmail_send", "bob@example.com", "expires=", "source="} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("list missing %q: %s", want, stdout.String())
		}
	}
	for range 2 {
		if code := Run(context.Background(), []string{"standing-auth", "revoke", id}, opts); code != 0 {
			t.Fatalf("revoke exit %d: %s", code, stderr.String())
		}
	}
	stdout.Reset()
	if code := Run(context.Background(), []string{"standing-auth", "list"}, opts); code != 0 || !strings.Contains(stdout.String(), "No standing grants") {
		t.Fatalf("after revoke exit %d: %s %s", code, stdout.String(), stderr.String())
	}
	// Explicit expiry is an alternative to TTL.
	args := standingAddArgs()
	args[8], args[9] = "--expires", time.Now().Add(time.Hour).Format(time.RFC3339)
	if code := Run(context.Background(), append([]string{"standing-auth", "add"}, args...), opts); code != 0 {
		t.Fatalf("expiry exit %d: %s", code, stderr.String())
	}
}

func TestStandingAuthAddRejectsMissingAndVague(t *testing.T) {
	for _, field := range []string{"--subject", "--tools", "--consequences", "--arguments", "--ttl", "--source-quote"} {
		t.Run(field, func(t *testing.T) {
			args := standingAddArgs()
			for i := 0; i < len(args); i += 2 {
				if args[i] == field {
					args = append(args[:i], args[i+2:]...)
					break
				}
			}
			assertStandingAddRejected(t, args)
		})
	}
	for _, phrase := range []string{"你看着办", "随便", "you figure it out", "Do What You Think Is Best", "whatever", "as you like"} {
		t.Run(phrase, func(t *testing.T) {
			args := standingAddArgs()
			args[len(args)-1] = phrase
			assertStandingAddRejected(t, args)
		})
	}
	for _, value := range []string{"{}", "null", `"organize my life"`, `{"goal":"whatever"}`} {
		args := standingAddArgs()
		args[7] = value
		assertStandingAddRejected(t, args)
	}
	for _, extra := range [][]string{{"--expires", "2030-01-01T00:00:00Z"}, {"--ttl", "0"}, {"--ttl", "-1h"}, {"a broad goal"}} {
		assertStandingAddRejected(t, append(standingAddArgs(), extra...))
	}
}

func assertStandingAddRejected(t *testing.T, args []string) {
	t.Helper()
	store := standingauth.New(filepath.Join(t.TempDir(), "grants"))
	var stdout, stderr strings.Builder
	if code := addStandingAuth(store, args, &stdout, &stderr); code != 2 || stderr.Len() == 0 {
		t.Fatalf("exit=%d output=%s error=%s", code, stdout.String(), stderr.String())
	}
	if grants, err := store.List(); err != nil || len(grants) != 0 {
		t.Fatalf("invalid grant persisted: %+v %v", grants, err)
	}
}
