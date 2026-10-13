package sticky

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// recorder keeps every set of pins handed to the save hook.
type recorder struct {
	saves [][]SavedPin
}

func (r *recorder) save(pins []SavedPin) {
	r.saves = append(r.saves, pins)
}

func (r *recorder) last(t *testing.T) []SavedPin {
	t.Helper()
	if len(r.saves) == 0 {
		t.Fatal("nothing saved")
	}
	return r.saves[len(r.saves)-1]
}

func TestPinFileRoundTrip(t *testing.T) {
	file := NewPinFile(filepath.Join(t.TempDir(), "pins.json"))
	if pins, err := file.Load(); err != nil || pins != nil {
		t.Fatalf("Load() of a missing file = %v, %v; want no pins, no error", pins, err)
	}

	want := []SavedPin{
		{Provider: "claude", Model: "opus", AuthID: "claude-1-a@example.com.json", PinnedAt: t0},
		{Provider: "codex", Model: "gpt", AuthID: "codex-2-b@example.com-pro.json", PinnedAt: t0.Add(time.Hour)},
	}
	if err := file.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}

	info, err := os.Stat(file.path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	if leftovers, _ := filepath.Glob(file.path + ".*.tmp"); len(leftovers) != 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestPinFileRejectsUnreadableContent(t *testing.T) {
	for name, content := range map[string]string{
		"not json":      "{",
		"wrong version": `{"version":2,"pins":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pins.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := NewPinFile(path).Load(); err == nil {
				t.Fatal("Load() succeeded, want error")
			}
		})
	}
}

func TestPinFileDropsIncompleteEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	content := `{"version":1,"pins":[
		{"provider":"Claude","model":"Opus(high)","auth_id":"a","pinned_at":"2026-10-09T12:00:00Z"},
		{"provider":"claude","model":"haiku","auth_id":""},
		{"provider":"","model":"sonnet","auth_id":"b"}
	]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := NewPinFile(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []SavedPin{{Provider: "claude", Model: "opus", AuthID: "a", PinnedAt: t0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestRestoredPinSurvivesRestart(t *testing.T) {
	file := NewPinFile(filepath.Join(t.TempDir(), "pins.json"))
	save := func(pins []SavedPin) {
		if err := file.Save(pins); err != nil {
			t.Fatal(err)
		}
	}

	before := NewScheduler(DefaultConfig(), nil)
	before.Persist(nil, save)
	mustPick(t, before, request("m", primary, backup), t0, "primary")
	mustPick(t, before, request("m", backup), t0.Add(time.Minute), "backup")
	mustPick(t, before, request("m", backup), t0.Add(time.Minute+DefaultSwitchAfter), "backup")

	// A restart with primary recovered: without the file the route would go back to primary.
	saved, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	after := NewScheduler(DefaultConfig(), nil)
	after.Persist(saved, save)
	mustPick(t, after, request("m", primary, backup), t0.Add(time.Hour), "backup")

	pins := after.Pins()
	if len(pins) != 1 || !pins[0].PinnedAt.Equal(t0.Add(time.Minute+DefaultSwitchAfter)) {
		t.Fatalf("Pins() = %+v, want the restored backup pin with its original pinned time", pins)
	}
}

func TestPersistSavesOnlyPinChanges(t *testing.T) {
	var rec recorder
	s := NewScheduler(DefaultConfig(), nil)
	s.Persist(nil, rec.save)
	if len(rec.saves) != 1 || len(rec.saves[0]) != 0 {
		t.Fatalf("saves after Persist = %+v, want one empty save", rec.saves)
	}

	mustPick(t, s, request("m", primary, backup), t0, "primary")
	if got := rec.last(t); len(got) != 1 || got[0].AuthID != "primary" {
		t.Fatalf("save after first pin = %+v", got)
	}

	// Picks that keep the pin, and a short absence, write nothing.
	mustPick(t, s, request("m", primary, backup), t0.Add(time.Second), "primary")
	mustPick(t, s, request("m", backup), t0.Add(2*time.Second), "backup")
	if len(rec.saves) != 2 {
		t.Fatalf("saves = %d, want 2", len(rec.saves))
	}

	mustPick(t, s, request("m", backup), t0.Add(2*time.Second+DefaultSwitchAfter), "backup")
	if got := rec.last(t); len(rec.saves) != 3 || got[0].AuthID != "backup" {
		t.Fatalf("save after move = %+v (saves %d)", got, len(rec.saves))
	}

	// Detaching stops saving.
	s.Persist(nil, nil)
	mustPick(t, s, request("other", primary, backup), t0.Add(time.Hour), "primary")
	if len(rec.saves) != 3 {
		t.Fatalf("saves after detach = %d, want 3", len(rec.saves))
	}
}

func TestPersistKeepsLivePinsOverSavedOnes(t *testing.T) {
	var rec recorder
	s := NewScheduler(DefaultConfig(), nil)
	mustPick(t, s, request("m", primary, backup), t0, "primary")

	s.Persist([]SavedPin{
		{Provider: "claude", Model: "m", AuthID: "backup", PinnedAt: t0.Add(-time.Hour)},
		{Provider: "claude", Model: "other", AuthID: "backup", PinnedAt: t0.Add(-time.Hour)},
	}, rec.save)

	mustPick(t, s, request("m", primary, backup), t0.Add(time.Minute), "primary")
	mustPick(t, s, request("other", primary, backup), t0.Add(time.Minute), "backup")
	want := []SavedPin{
		{Provider: "claude", Model: "m", AuthID: "primary", PinnedAt: t0},
		{Provider: "claude", Model: "other", AuthID: "backup", PinnedAt: t0.Add(-time.Hour)},
	}
	if got := rec.last(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved = %+v, want %+v", got, want)
	}
}

func TestRestoredPinToRemovedCredentialMovesAfterGrace(t *testing.T) {
	s := NewScheduler(DefaultConfig(), &fakeInspector{states: map[string]AuthState{}})
	s.Persist([]SavedPin{{Provider: "claude", Model: "m", AuthID: "removed", PinnedAt: t0.Add(-time.Hour)}}, nil)

	if moved := mustPick(t, s, request("m", primary, backup), t0, "primary"); moved != nil {
		t.Fatalf("moved before the grace period: %+v", moved)
	}
	moved := mustPick(t, s, request("m", primary, backup), t0.Add(DefaultSwitchAfter), "primary")
	if moved == nil || moved.From != "removed" || moved.To != "primary" || moved.Reason != ReasonGraceExpired {
		t.Fatalf("switch = %+v, want removed -> primary after the grace period", moved)
	}
}
