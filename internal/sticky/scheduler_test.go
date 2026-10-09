package sticky

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var t0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func candidate(id string, priority int) pluginapi.SchedulerAuthCandidate {
	return pluginapi.SchedulerAuthCandidate{ID: id, Provider: "claude", Priority: priority}
}

func request(model string, candidates ...pluginapi.SchedulerAuthCandidate) pluginapi.SchedulerPickRequest {
	return pluginapi.SchedulerPickRequest{
		Provider:   "claude",
		Providers:  []string{"claude"},
		Model:      model,
		Candidates: candidates,
	}
}

func mustPick(t *testing.T, s *Scheduler, req pluginapi.SchedulerPickRequest, now time.Time, want string) *Switch {
	t.Helper()
	resp, moved := s.Pick(req, now)
	if !resp.Handled {
		t.Fatalf("Pick() unhandled, want %q", want)
	}
	if resp.AuthID != want {
		t.Fatalf("Pick() = %q, want %q", resp.AuthID, want)
	}
	return moved
}

var (
	primary = candidate("primary", 10)
	backup  = candidate("backup", 0)
)

func TestFirstPickFollowsFillFirstOrder(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("m", backup, primary), t0, "primary")

	tied := NewScheduler(DefaultConfig(), nil)
	mustPick(t, tied, request("m", candidate("b", 1), candidate("a", 1)), t0, "a")
}

func TestStaysOnFailoverCredentialAfterPrimaryRecovers(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("m", primary, backup), t0, "primary")

	// Primary runs out of quota and drops out of the candidates.
	mustPick(t, s, request("m", backup), t0.Add(time.Minute), "backup")
	moved := mustPick(t, s, request("m", backup), t0.Add(time.Minute+DefaultSwitchAfter), "backup")
	if moved == nil || moved.From != "primary" || moved.To != "backup" || moved.Reason != ReasonGraceExpired || moved.MissingFor != DefaultSwitchAfter {
		t.Fatalf("switch = %+v, want primary -> backup after %s", moved, DefaultSwitchAfter)
	}

	// Primary's window resets; traffic must stay on backup.
	if moved := mustPick(t, s, request("m", primary, backup), t0.Add(time.Hour), "backup"); moved != nil {
		t.Fatalf("unexpected switch %+v", moved)
	}

	// Backup runs out next; the pin moves back to primary after the grace period.
	mustPick(t, s, request("m", primary), t0.Add(2*time.Hour), "primary")
	moved = mustPick(t, s, request("m", primary), t0.Add(2*time.Hour+DefaultSwitchAfter), "primary")
	if moved == nil || moved.From != "backup" || moved.To != "primary" {
		t.Fatalf("switch = %+v, want backup -> primary", moved)
	}
	mustPick(t, s, request("m", primary, backup), t0.Add(3*time.Hour), "primary")
}

func TestShortAbsenceKeepsPin(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("m", primary, backup), t0, "primary")

	// A retry that already tried primary, or a one-minute transient cooldown.
	mustPick(t, s, request("m", backup), t0.Add(time.Second), "backup")
	mustPick(t, s, request("m", primary, backup), t0.Add(2*time.Second), "primary")

	// The absence timer restarted when primary reappeared.
	if moved := mustPick(t, s, request("m", backup), t0.Add(DefaultSwitchAfter), "backup"); moved != nil {
		t.Fatalf("switched after a fresh absence: %+v", moved)
	}
}

func TestZeroSwitchAfterMovesImmediately(t *testing.T) {
	cfg, err := ParseConfig([]byte("switch_after: 0s\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(cfg, nil)
	mustPick(t, s, request("m", primary, backup), t0, "primary")
	if moved := mustPick(t, s, request("m", backup), t0, "backup"); moved == nil {
		t.Fatal("want immediate switch")
	}
	mustPick(t, s, request("m", primary, backup), t0, "backup")
}

func TestRoutesArePinnedPerModelIgnoringThinkingSuffix(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("opus", primary, backup), t0, "primary")
	mustPick(t, s, request("opus", backup), t0.Add(time.Minute), "backup")
	mustPick(t, s, request("opus", backup), t0.Add(time.Minute+DefaultSwitchAfter), "backup")

	mustPick(t, s, request("Opus(high)", primary, backup), t0.Add(time.Hour), "backup")
	mustPick(t, s, request("haiku", primary, backup), t0.Add(time.Hour), "primary")
}

func TestOutOfScopeRequestsAreUnhandled(t *testing.T) {
	cfg, err := ParseConfig([]byte("providers: [Claude]\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(cfg, nil)

	codex := request("m", primary, backup)
	codex.Provider, codex.Providers = "codex", []string{"codex"}
	mixed := request("m", primary, backup)
	mixed.Provider, mixed.Providers = "", []string{"claude", "codex"}

	cases := map[string]pluginapi.SchedulerPickRequest{
		"provider not configured": codex,
		"mixed providers":         mixed,
		"no candidates":           request("m"),
	}
	for name, req := range cases {
		if resp, _ := s.Pick(req, t0); resp.Handled {
			t.Errorf("%s: Pick() handled = %+v, want unhandled", name, resp)
		}
	}
	mustPick(t, s, request("m", primary, backup), t0, "primary")
}

func TestSingleProviderOnMixedPathIsHandled(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	req := request("m", primary, backup)
	req.Provider = ""
	mustPick(t, s, req, t0, "primary")
	mustPick(t, s, request("m", backup), t0.Add(time.Minute), "backup")
	mustPick(t, s, request("m", backup), t0.Add(time.Minute+DefaultSwitchAfter), "backup")
	mustPick(t, s, req, t0.Add(time.Hour), "backup")
}

func TestCodexWebsocketPoolsAreUnhandled(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	ws := pluginapi.SchedulerAuthCandidate{ID: "ws", Provider: "codex", Attributes: map[string]string{"websockets": "true"}}
	req := pluginapi.SchedulerPickRequest{Provider: "codex", Providers: []string{"codex"}, Model: "m", Candidates: []pluginapi.SchedulerAuthCandidate{ws, backup}}
	if resp, _ := s.Pick(req, t0); resp.Handled {
		t.Fatalf("Pick() handled = %+v, want unhandled", resp)
	}

	ws.Attributes["websockets"] = "false"
	mustPick(t, s, req, t0, "backup")
}

func TestConfigureKeepsPins(t *testing.T) {
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("m", primary, backup), t0, "primary")
	mustPick(t, s, request("m", backup), t0.Add(time.Minute), "backup")
	mustPick(t, s, request("m", backup), t0.Add(time.Minute+DefaultSwitchAfter), "backup")

	s.Configure(DefaultConfig())
	mustPick(t, s, request("m", primary, backup), t0.Add(time.Hour), "backup")
}
