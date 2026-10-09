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
- If the pinned credential is missing from the available candidates, the request goes to the `fill-first` choice: highest priority first, then lowest auth ID. The pin moves to that choice only after the pinned credential has been missing for `switch_after` (default `5m`). A retry after a single upstream error, or CLIProxyAPI's one-minute cooldown after a transient error, does not move the pin. Running out of quota, which puts the credential in cooldown until the quota window resets, does.
- On a route with no pin yet, the first request is served by the `fill-first` choice, which becomes the pin.
- When a pin moves, the plugin logs at info level: `sticky-fill-first: pin moved from <old> to <new> after 5m0s unavailable provider=claude model=...`.

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
- It decides only from which credentials CLIProxyAPI reports as available. It does not read quota or rate-limit headers, and it makes no requests of its own.

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
