package sticky

import (
	"testing"
	"time"
)

func TestParseConfig(t *testing.T) {
	cfg, err := ParseConfig([]byte("enabled: true\npriority: 1\nproviders: [' Claude ', codex, '']\nswitch_after: 90s\npins_file: /var/lib/cpa//pins.json\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SwitchAfter() != 90*time.Second {
		t.Errorf("SwitchAfter() = %s, want 90s", cfg.SwitchAfter())
	}
	if cfg.PinsFile() != "/var/lib/cpa/pins.json" {
		t.Errorf("PinsFile() = %q, want /var/lib/cpa/pins.json", cfg.PinsFile())
	}
	for provider, want := range map[string]bool{"claude": true, "CODEX": true, "gemini": false} {
		if got := cfg.Applies(provider); got != want {
			t.Errorf("Applies(%q) = %v, want %v", provider, got, want)
		}
	}
}

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig([]byte("enabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SwitchAfter() != DefaultSwitchAfter {
		t.Errorf("SwitchAfter() = %s, want %s", cfg.SwitchAfter(), DefaultSwitchAfter)
	}
	if cfg.PinsFile() != "" {
		t.Errorf("PinsFile() = %q, want in-memory pins by default", cfg.PinsFile())
	}
	if !cfg.Applies("anything") {
		t.Error("empty providers must apply to every provider")
	}
}

func TestParseConfigRejectsInvalidValues(t *testing.T) {
	for _, raw := range []string{"switch_after: soon\n", "switch_after: -1m\n", "providers: claude\n", "pins_file: pins.json\n"} {
		if _, err := ParseConfig([]byte(raw)); err == nil {
			t.Errorf("ParseConfig(%q) succeeded, want error", raw)
		}
	}
}
