package proactive

import (
	"testing"
	"time"

	"github.com/hszjj221/gg/internal/agent"
	"github.com/hszjj221/gg/internal/config"
)

func TestDecideLocalDelivery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profile  string
		scenario Scenario
		later    bool
		notify   bool
		want     Decision
	}{
		{"default coding", "", EmailSummary, false, false, Quiet},
		{"coding", config.ProfileCoding, CalendarReminder, false, false, Quiet},
		{"coding cannot capture", config.ProfileCoding, EmailSummary, true, false, Quiet},
		{"unknown profile", "other", EmailSummary, false, false, Quiet},
		{"personal email", config.ProfilePersonal, EmailSummary, false, false, Summarize},
		{"personal calendar", config.ProfilePersonal, CalendarReminder, false, false, Summarize},
		{"remember later", config.ProfilePersonal, EmailSummary, true, false, NoteOnly},
		{"authorization check", config.ProfilePersonal, StandingAuthCheck, false, false, Quiet},
		{"unknown scenario", config.ProfilePersonal, "other", false, false, Quiet},
		{"notify default off", config.ProfilePersonal, CalendarReminder, false, true, Quiet},
		{"capture cannot enable notify", config.ProfilePersonal, EmailSummary, true, true, Quiet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (Policy{}).Decide(Input{Profile: tc.profile, Scenario: tc.scenario, RememberLater: tc.later, Notify: tc.notify})
			if got.Decision != tc.want || got.Reason == "" {
				t.Fatalf("Decide = %+v, want %s and a reason", got, tc.want)
			}
		})
	}
}

func TestQuietHours(t *testing.T) {
	for _, tc := range []struct {
		window string
		at     string
		later  bool
		want   Decision
	}{
		{"", "23:00", false, Summarize},
		{"22:00-07:00", "21:59", false, Summarize},
		{"22:00-07:00", "22:00", false, Quiet},
		{"22:00-07:00", "23:59", false, Quiet},
		{"22:00-07:00", "00:00", false, Quiet},
		{"22:00-07:00", "06:59", false, Quiet},
		{"22:00-07:00", "07:00", false, Summarize},
		{"09:00-17:00", "08:59", false, Summarize},
		{"09:00-17:00", "09:00", false, Quiet},
		{"09:00-17:00", "16:59", false, Quiet},
		{"09:00-17:00", "17:00", false, Summarize},
		{"22:00-07:00", "23:00", true, NoteOnly},
	} {
		t.Run(tc.window+"/"+tc.at+"/"+string(tc.want), func(t *testing.T) {
			// Local wall time, not UTC: 23:00 here is 15:00 UTC.
			loc := time.FixedZone("demo local", 8*60*60)
			now, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-24 "+tc.at, loc)
			if err != nil {
				t.Fatal(err)
			}
			p := Policy{QuietHours: tc.window}
			if err := p.Validate(); err != nil {
				t.Fatal(err)
			}
			got := p.Decide(Input{Profile: config.ProfilePersonal, Scenario: CalendarReminder, Now: now, RememberLater: tc.later})
			if got.Decision != tc.want {
				t.Fatalf("Decide = %+v, want %s", got, tc.want)
			}
		})
	}
	for _, window := range []string{"22:00", "22:00-", "-07:00", "24:00-07:00", "22:60-07:00", "7:00-22:00", "07:00-07:00", "22:00-07:00-extra"} {
		p := Policy{QuietHours: window}
		if p.Validate() == nil || p.Decide(Input{Profile: config.ProfilePersonal, Scenario: EmailSummary}).Decision != Quiet {
			t.Errorf("invalid window %q did not fail quiet", window)
		}
	}
	if got := (Policy{QuietHours: "22:00-07:00"}).Decide(Input{Profile: config.ProfilePersonal, Scenario: EmailSummary}); got.Decision != Quiet {
		t.Fatalf("missing clock should fail quiet: %+v", got)
	}
}

func TestPolicyNeverAuthorizesTools(t *testing.T) {
	// Check all combinations of known E/M/U, including R=true and a read-only
	// risk label. Remember-later cannot convert a gated action into a note.
	for mask := range 8 {
		req := agent.ApprovalRequest{
			Reversible: new(true), Risk: agent.ApprovalRiskReadOnly,
			ExternallyVisible: new(mask&1 != 0), SpendsMoneyOrChangesPermissions: new(mask&2 != 0), ActsInUserName: new(mask&4 != 0),
		}
		for _, later := range []bool{false, true} {
			got := (Policy{}).Decide(Input{Profile: config.ProfilePersonal, Scenario: EmailSummary, RememberLater: later, ProposedAction: &req})
			if got.Decision != Quiet {
				t.Fatalf("effects mask=%d later=%v: %+v", mask, later, got)
			}
		}
	}
	// Missing consequence metadata is not evidence of a local action.
	if got := (Policy{}).Decide(Input{Profile: config.ProfilePersonal, Scenario: EmailSummary, ProposedAction: &agent.ApprovalRequest{}}); got.Decision != Quiet {
		t.Fatalf("unknown tool effects should stay quiet: %+v", got)
	}
}
