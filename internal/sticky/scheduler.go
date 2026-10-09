// Package sticky implements sticky fill-first credential selection for CLIProxyAPI.
//
// Built-in fill-first is stateless: every pick goes to the highest-priority available
// credential, so traffic returns to that credential as soon as its quota window resets.
// Sticky fill-first pins each provider+model route to the credential that last served it
// and keeps it there while it stays available. The pin moves only after the pinned
// credential has been unavailable for the configured grace period, and then it moves to
// the fill-first choice (highest priority, then lowest auth ID) among available credentials.
package sticky

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Switch describes a pin that moved to another credential.
type Switch struct {
	Provider   string
	Model      string
	From       string
	To         string
	MissingFor time.Duration
}

type routeKey struct {
	provider string
	model    string
}

type pin struct {
	authID string
	// missingSince is when a pick first found authID absent from the candidates; zero while present.
	missingSince time.Time
}

// Scheduler holds the pins for every route. It is safe for concurrent use.
type Scheduler struct {
	mu   sync.Mutex
	cfg  Config
	pins map[routeKey]*pin
}

// NewScheduler returns a scheduler with no pins.
func NewScheduler(cfg Config) *Scheduler {
	return &Scheduler{cfg: cfg, pins: make(map[routeKey]*pin)}
}

// Configure replaces the configuration and keeps existing pins.
func (s *Scheduler) Configure(cfg Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
}

// Pick chooses a candidate for the request. It returns Handled=false when the request is out of
// scope, so the host falls back to its configured built-in strategy. The returned Switch is
// non-nil only when this pick moved a pin.
func (s *Scheduler) Pick(req pluginapi.SchedulerPickRequest, now time.Time) (pluginapi.SchedulerPickResponse, *Switch) {
	unhandled := pluginapi.SchedulerPickResponse{}

	provider, ok := routeProvider(req)
	if !ok || len(req.Candidates) == 0 {
		return unhandled, nil
	}
	// The host cannot tell the plugin whether a Codex request arrived over a downstream
	// websocket, so leave websocket-capable Codex pools to the built-in websocket preference.
	if provider == "codex" && anyWebsocketCandidate(req.Candidates) {
		return unhandled, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cfg.Applies(provider) {
		return unhandled, nil
	}

	key := routeKey{provider: provider, model: baseModel(req.Model)}
	fallback := fillFirst(req.Candidates)
	current, pinned := s.pins[key]
	if !pinned {
		s.pins[key] = &pin{authID: fallback}
		return handled(fallback), nil
	}

	if containsCandidate(req.Candidates, current.authID) {
		current.missingSince = time.Time{}
		return handled(current.authID), nil
	}

	// Absent for this pick only: a retry that already tried the pinned credential, a request
	// restricted to other credentials, or a short cooldown. Serve the fill-first choice
	// without moving the pin until the absence outlasts the grace period.
	if current.missingSince.IsZero() {
		current.missingSince = now
	}
	missingFor := now.Sub(current.missingSince)
	if missingFor < s.cfg.SwitchAfter() {
		return handled(fallback), nil
	}

	moved := &Switch{
		Provider:   key.provider,
		Model:      key.model,
		From:       current.authID,
		To:         fallback,
		MissingFor: missingFor,
	}
	s.pins[key] = &pin{authID: fallback}
	return handled(fallback), moved
}

// routeProvider returns the single provider a request routes to. The host leaves Provider
// empty on its mixed-provider path even when Providers holds one entry; routes spanning
// several providers have no single provider to pin against.
func routeProvider(req pluginapi.SchedulerPickRequest) (string, bool) {
	if provider := normalizeProvider(req.Provider); provider != "" {
		return provider, len(req.Providers) <= 1
	}
	if len(req.Providers) == 1 {
		provider := normalizeProvider(req.Providers[0])
		return provider, provider != ""
	}
	return "", false
}

func handled(authID string) pluginapi.SchedulerPickResponse {
	return pluginapi.SchedulerPickResponse{AuthID: authID, Handled: true}
}

// fillFirst mirrors CLIProxyAPI's built-in fill-first order: highest priority, then lowest ID.
// candidates must be non-empty.
func fillFirst(candidates []pluginapi.SchedulerAuthCandidate) string {
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Priority > best.Priority || (candidate.Priority == best.Priority && candidate.ID < best.ID) {
			best = candidate
		}
	}
	return best.ID
}

func containsCandidate(candidates []pluginapi.SchedulerAuthCandidate, authID string) bool {
	for _, candidate := range candidates {
		if candidate.ID == authID {
			return true
		}
	}
	return false
}

func anyWebsocketCandidate(candidates []pluginapi.SchedulerAuthCandidate) bool {
	for _, candidate := range candidates {
		enabled, errParse := strconv.ParseBool(strings.TrimSpace(candidate.Attributes["websockets"]))
		if errParse == nil && enabled {
			return true
		}
	}
	return false
}

// baseModel drops a CLIProxyAPI thinking suffix such as "(high)" so every reasoning level of a
// model shares one pin, matching how the host matches credentials to models.
func baseModel(model string) string {
	model = strings.TrimSpace(model)
	if open := strings.LastIndex(model, "("); open > 0 && strings.HasSuffix(model, ")") {
		model = strings.TrimSpace(model[:open])
	}
	return strings.ToLower(model)
}
