package sticky

import (
	"errors"
	"testing"
	"time"
)

// fakeInspector reports fixed host state and counts lookups.
type fakeInspector struct {
	states map[string]AuthState
	calls  int
}

func (f *fakeInspector) Inspect(authID string) (AuthState, bool) {
	f.calls++
	state, ok := f.states[authID]
	return state, ok
}

func pinnedScheduler(t *testing.T, inspector Inspector) *Scheduler {
	t.Helper()
	s := NewScheduler(DefaultConfig(), inspector)
	mustPick(t, s, request("m", primary, backup), t0, "primary")
	return s
}

func TestHostLockoutMovesPinImmediately(t *testing.T) {
	cases := map[string]struct {
		state  AuthState
		reason Reason
	}{
		"quota cooldown until the window resets": {AuthState{NextRetryAfter: t0.Add(5 * time.Hour)}, ReasonCooldown},
		"disabled credential":                    {AuthState{Disabled: true}, ReasonDisabled},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := pinnedScheduler(t, &fakeInspector{states: map[string]AuthState{"primary": tc.state}})
			moved := mustPick(t, s, request("m", backup), t0.Add(time.Second), "backup")
			if moved == nil || moved.Reason != tc.reason || moved.From != "primary" || moved.To != "backup" {
				t.Fatalf("switch = %+v, want primary -> backup with reason %d", moved, tc.reason)
			}
			if tc.reason == ReasonCooldown && !moved.Until.Equal(tc.state.NextRetryAfter) {
				t.Fatalf("Until = %s, want %s", moved.Until, tc.state.NextRetryAfter)
			}
			// The idle gap: the primary resets before the next request and must not win it back.
			mustPick(t, s, request("m", primary, backup), t0.Add(6*time.Hour), "backup")
		})
	}
}

func TestInconclusiveHostStateKeepsPin(t *testing.T) {
	cases := map[string]*fakeInspector{
		"transient cooldown":            {states: map[string]AuthState{"primary": {NextRetryAfter: t0.Add(time.Minute)}}},
		"30m auth-error cooldown":       {states: map[string]AuthState{"primary": {NextRetryAfter: t0.Add(time.Second + QuotaCooldownFloor)}}},
		"cooldown already over":         {states: map[string]AuthState{"primary": {NextRetryAfter: t0.Add(-time.Minute)}}},
		"no cooldown (retried request)": {states: map[string]AuthState{"primary": {}}},
		"credential unknown to host":    {states: map[string]AuthState{}},
	}
	for name, inspector := range cases {
		t.Run(name, func(t *testing.T) {
			s := pinnedScheduler(t, inspector)
			if moved := mustPick(t, s, request("m", backup), t0.Add(time.Second), "backup"); moved != nil {
				t.Fatalf("unexpected switch %+v", moved)
			}
			mustPick(t, s, request("m", primary, backup), t0.Add(2*time.Second), "primary")
		})
	}
}

func TestGracePeriodStillAppliesWithInspector(t *testing.T) {
	s := pinnedScheduler(t, &fakeInspector{states: map[string]AuthState{}})
	mustPick(t, s, request("m", backup), t0.Add(time.Second), "backup")
	moved := mustPick(t, s, request("m", backup), t0.Add(time.Second+DefaultSwitchAfter), "backup")
	if moved == nil || moved.Reason != ReasonGraceExpired {
		t.Fatalf("switch = %+v, want grace-expired switch", moved)
	}
}

func TestLockoutThresholdFollowsLongerGracePeriod(t *testing.T) {
	cfg, err := ParseConfig([]byte("switch_after: 2h\n"))
	if err != nil {
		t.Fatal(err)
	}
	inspector := &fakeInspector{states: map[string]AuthState{"primary": {NextRetryAfter: t0.Add(time.Hour)}}}
	s := NewScheduler(cfg, inspector)
	mustPick(t, s, request("m", primary, backup), t0, "primary")
	if moved := mustPick(t, s, request("m", backup), t0.Add(time.Second), "backup"); moved != nil {
		t.Fatalf("a 1h cooldown must not beat a 2h grace period: %+v", moved)
	}
}

func TestPresentPinDoesNotCallHost(t *testing.T) {
	inspector := &fakeInspector{}
	s := pinnedScheduler(t, inspector)
	mustPick(t, s, request("m", primary, backup), t0.Add(time.Second), "primary")
	if inspector.calls != 0 {
		t.Fatalf("host called %d times while the pin was available", inspector.calls)
	}
}

func TestCachedInspectorReusesListingWithinTTL(t *testing.T) {
	now := t0
	calls := 0
	list := func() (map[string]AuthState, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("host unavailable")
		}
		return map[string]AuthState{"a": {Disabled: true}}, nil
	}
	c := NewCachedInspector(list, 2*time.Second)
	c.now = func() time.Time { return now }

	if state, ok := c.Inspect("a"); !ok || !state.Disabled {
		t.Fatalf("Inspect(a) = %+v, %v", state, ok)
	}
	if _, ok := c.Inspect("missing"); ok {
		t.Fatal("unknown credential must report ok=false")
	}
	now = now.Add(time.Second)
	c.Inspect("a")
	if calls != 1 {
		t.Fatalf("listing calls = %d within TTL, want 1", calls)
	}

	now = now.Add(2 * time.Second)
	if _, ok := c.Inspect("a"); ok {
		t.Fatal("failed listing must report ok=false")
	}
	c.Inspect("a")
	if calls != 2 {
		t.Fatalf("failed listing must be cached for the TTL, calls = %d", calls)
	}
}
