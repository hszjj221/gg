package cliapp

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hszjj221/gg/internal/config"
	"github.com/hszjj221/gg/internal/standingauth"
)

const standingAuthUsage = `usage:
  gg standing-auth list
  gg standing-auth add --subject "gg on behalf of ACCOUNT" --tools TOOL[,TOOL] --consequences E,M,U|none --arguments '{"resource":"exact value"}' (--ttl 24h | --expires RFC3339) --source-quote "USER AUTHORIZATION" [--notes TEXT]
  gg standing-auth revoke <id>`

func runStandingAuthCommand(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, standingAuthUsage)
		return 2
	}
	store := standingauth.New(standingauth.DefaultDir(cfg.HomeDir))
	switch args[0] {
	case "list":
		if len(args) != 1 {
			fmt.Fprintln(stderr, standingAuthUsage)
			return 2
		}
		grants, err := store.List()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if len(grants) == 0 {
			fmt.Fprintln(stdout, "No standing grants.")
		}
		for _, g := range grants {
			scope, _ := json.Marshal(g.Scope)
			status := "active"
			if !time.Now().Before(g.ExpiresAt) {
				status = "expired"
			}
			fmt.Fprintf(stdout, "%s  subject=%q  scope=%s  expires=%s (%s)  source=%q\n", g.ID, g.Subject, scope, g.ExpiresAt.Format(time.RFC3339), status, g.SourceQuote)
		}
		return 0
	case "revoke":
		if len(args) != 2 {
			fmt.Fprintln(stderr, standingAuthUsage)
			return 2
		}
		if err := store.Revoke(args[1]); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, "Revoked (or already absent):", args[1])
		return 0
	case "add":
		return addStandingAuth(store, args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, standingAuthUsage)
		return 2
	}
}

func addStandingAuth(store *standingauth.Store, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gg standing-auth add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	subject := fs.String("subject", "", "explicit actor and user/account on whose behalf it acts")
	tools := fs.String("tools", "", "comma-separated exact tool names")
	effects := fs.String("consequences", "", "allowed effects: E,M,U or none")
	arguments := fs.String("arguments", "", "nonempty JSON object of exact argument limits")
	ttl := fs.Duration("ttl", 0, "validity duration, e.g. 24h")
	expires := fs.String("expires", "", "expiry timestamp in RFC3339 (instead of --ttl)")
	quote := fs.String("source-quote", "", "plain-text user authorization quote")
	notes := fs.String("notes", "", "optional notes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments; scope must use structured flags")
		return 2
	}
	var expiry time.Time
	var err error
	ttlSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ttl" {
			ttlSet = true
		}
	})
	if ttlSet && *expires != "" {
		fmt.Fprintln(stderr, "choose exactly one of --ttl or --expires")
		return 2
	}
	if ttlSet {
		if *ttl <= 0 {
			fmt.Fprintln(stderr, "--ttl must be positive")
			return 2
		}
		expiry = time.Now().UTC().Add(*ttl)
	} else if *expires != "" {
		expiry, err = time.Parse(time.RFC3339, *expires)
		if err != nil {
			fmt.Fprintln(stderr, "--expires must be an RFC3339 timestamp with timezone")
			return 2
		}
	}
	g := standingauth.Grant{Subject: *subject, ExpiresAt: expiry, SourceQuote: *quote, Notes: *notes}
	if *tools != "" {
		g.Scope.Tools = strings.Split(*tools, ",")
		for i := range g.Scope.Tools {
			g.Scope.Tools[i] = strings.TrimSpace(g.Scope.Tools[i])
		}
	}
	if *effects == "none" {
		g.Scope.Consequences = []string{}
	} else if *effects != "" {
		g.Scope.Consequences = strings.Split(*effects, ",")
		for i := range g.Scope.Consequences {
			g.Scope.Consequences[i] = strings.TrimSpace(g.Scope.Consequences[i])
		}
	}
	if *arguments != "" {
		if err := json.Unmarshal([]byte(*arguments), &g.Scope.Arguments); err != nil {
			fmt.Fprintln(stderr, "--arguments must be a nonempty JSON object of exact argument limits")
			return 2
		}
	}
	if err := g.Validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !expiry.After(time.Now()) {
		fmt.Fprintln(stderr, "expiry must be in the future")
		return 2
	}
	created, err := store.Add(g)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, created.ID)
	return 0
}
