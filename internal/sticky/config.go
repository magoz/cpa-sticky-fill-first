package sticky

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultSwitchAfter is how long a pinned credential must stay unavailable before the pin moves.
// It outlasts CLIProxyAPI's one-minute transient-error cooldown, so a single 5xx does not move
// the pin, while quota exhaustion (cooldowns of hours) does.
const DefaultSwitchAfter = 5 * time.Minute

// Config is a validated plugin configuration. Build it with ParseConfig.
type Config struct {
	// providers restricts sticky routing to these provider keys; nil means every provider.
	providers   map[string]struct{}
	switchAfter time.Duration
	// pinsFile is an absolute path where pins are saved across restarts; empty keeps them in memory only.
	pinsFile string
}

// DefaultConfig applies sticky routing to every provider with DefaultSwitchAfter.
func DefaultConfig() Config {
	return Config{switchAfter: DefaultSwitchAfter}
}

// SwitchAfter reports the grace period before an unavailable pinned credential is replaced.
func (c Config) SwitchAfter() time.Duration {
	return c.switchAfter
}

// PinsFile reports where pins are saved, or "" when they live in memory only.
func (c Config) PinsFile() string {
	return c.pinsFile
}

// Applies reports whether sticky routing handles the given provider key.
func (c Config) Applies(provider string) bool {
	if c.providers == nil {
		return true
	}
	_, ok := c.providers[normalizeProvider(provider)]
	return ok
}

type rawConfig struct {
	Providers   []string `yaml:"providers"`
	SwitchAfter string   `yaml:"switch_after"`
	PinsFile    string   `yaml:"pins_file"`
}

// ParseConfig decodes the plugins.configs.sticky-fill-first YAML block. Unknown keys such as
// enabled and priority belong to the host and are ignored.
func ParseConfig(raw []byte) (Config, error) {
	var parsed rawConfig
	if errUnmarshal := yaml.Unmarshal(raw, &parsed); errUnmarshal != nil {
		return Config{}, fmt.Errorf("decode config: %w", errUnmarshal)
	}

	cfg := DefaultConfig()
	if value := strings.TrimSpace(parsed.SwitchAfter); value != "" {
		duration, errParse := time.ParseDuration(value)
		if errParse != nil {
			return Config{}, fmt.Errorf("switch_after: %w", errParse)
		}
		if duration < 0 {
			return Config{}, fmt.Errorf("switch_after must not be negative, got %s", value)
		}
		cfg.switchAfter = duration
	}

	if value := strings.TrimSpace(parsed.PinsFile); value != "" {
		// The host's working directory is not part of its contract, so a relative path has no
		// stable meaning.
		if !filepath.IsAbs(value) {
			return Config{}, fmt.Errorf("pins_file must be an absolute path, got %s", value)
		}
		cfg.pinsFile = filepath.Clean(value)
	}

	for _, provider := range parsed.Providers {
		key := normalizeProvider(provider)
		if key == "" {
			continue
		}
		if cfg.providers == nil {
			cfg.providers = make(map[string]struct{}, len(parsed.Providers))
		}
		cfg.providers[key] = struct{}{}
	}
	return cfg, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
