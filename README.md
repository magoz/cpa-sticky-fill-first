# Sticky Fill First

A [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) scheduler plugin. It stays on the credential that took over after a failover, and does not switch back when the higher-priority credential recovers.

Built-in `fill-first` sends every request to the highest-priority available credential, so traffic moves back to the top account as soon as its quota window resets. In a pool of subscription accounts, that drains the first account over and over while the others sit idle. This plugin keeps traffic on the account currently serving and only moves on when that account runs out:

```text
fill-first:        A A A ✗ B B B (A resets) A A A ✗ B ...
sticky-fill-first: A A A ✗ B B B (A resets) B B B ✗ A ...
```

Plugin ID: `sticky-fill-first`. Requires CLIProxyAPI 7.3.8 or newer (scheduler plugins that see every priority tier).

## Behavior

- Every provider and model route (for example `claude` + `claude-opus-5-5`) has a pin: the credential that last served it. A thinking suffix such as `(high)` shares the pin of its base model.
- While the pinned credential is available, it serves every request on that route, even when a higher-priority credential is also available.
- If the pinned credential is missing from the available candidates, the request goes to the `fill-first` choice: highest priority first, then lowest auth ID. Whether the pin moves to that choice depends on why the credential is missing. The plugin asks CLIProxyAPI for the credential's runtime state (`host.auth.list`, at most once every 2 seconds):

  | Host state of the pinned credential | Pin |
  | --- | --- |
  | Cooldown ending more than `max(30m, switch_after)` from now: a provider quota reset, such as Anthropic's unified limit or Codex's usage limit | Moves now |
  | Disabled | Moves now |
  | Shorter cooldown: a transient error (1 minute), an auth error being refreshed (30 minutes), or quota backoff without a reset time | Stays, until the grace period expires |
  | No cooldown (the request already tried it, or is restricted to other credentials), or not reported by the host | Stays, until the grace period expires |

- The grace period is the fallback: the pin moves once the pinned credential has been missing for `switch_after` (default `5m`).
- The 30-minute floor is there because CLIProxyAPI caps every non-quota cooldown at 30 minutes, so only a real quota reset outlasts it.
- On a route with no pin yet, the first request is served by the `fill-first` choice, which becomes the pin.
- When a pin moves, the plugin logs at info level:
  - `sticky-fill-first: pin moved from <old> to <new>: in cooldown until 2026-10-09T21:00:00Z provider=claude model=...`
  - `... : disabled`
  - `... : unavailable for 5m0s`

Pins live in memory. Restarting CLIProxyAPI clears them, so routing goes back to priority order. Use a restart to return to your top-priority account deliberately.

## Configuration

```yaml
plugins:
  enabled: true
  configs:
    sticky-fill-first:
      enabled: true
      priority: 1
      # Provider keys to route stickily. Omit or leave empty to apply to every provider.
      providers: [claude, codex]
      # How long the pinned credential must stay unavailable before the pin moves (Go duration).
      switch_after: 5m
```

Credential `priority` values still decide the first pin and where the pin moves. Higher priority wins, as with `fill-first`.

## Scope and caveats

- For the providers it handles, the plugin takes precedence over `routing.strategy` and `routing.session-affinity`. Everything else keeps the configured strategy.
- The plugin leaves a request to the configured strategy when:
  - the route spans several providers;
  - the request is for a Codex pool that has a websocket-enabled credential, because plugins cannot see whether the client connected over a websocket, and the built-in strategy prefers websocket credentials for those clients;
  - the provider is not listed in `providers`.
- It decides from what CLIProxyAPI reports: the available candidates, and, when the pinned credential is missing, that credential's cooldown deadline and disabled flag from `host.auth.list`.
  - It ignores status messages, because they can contain raw upstream errors.
  - It logs no credential data other than auth IDs.
  - It does not parse quota headers and makes no requests of its own.
- Cooldowns on a single model, and credentials defined as config API keys rather than auth files, are not visible through `host.auth.list`. For those, the pin moves on the grace period only.

## Install

From the CLIProxyAPI plugin store, or manually: download the zip for your platform from the [latest release](https://github.com/magoz/cpa-sticky-fill-first/releases/latest). Check it against `checksums.txt`, then extract `sticky-fill-first.so`, `.dylib` or `.dll` into `<plugins.dir>/<goos>/<goarch>/` and restart CLIProxyAPI.

## Development

```bash
go test -race ./...
go build -buildmode=c-shared -o sticky-fill-first.so .
```

Routing logic lives in `internal/sticky` and has no cgo. `main.go` is the C ABI glue. Releases are built by `.github/workflows/build.yml` when a `v<version>` tag is pushed.

## License

MIT
