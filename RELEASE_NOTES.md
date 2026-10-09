## v0.3.0

- **New route:** `GET /v0/management/plugins/sticky-fill-first/pins` lists the current pin of every route. It is read-only and requires the management key. Each pin shows its provider, model, auth ID, when it was pinned, when it last served a request, and since when it has been unavailable, if it is.

## v0.2.0

- **Immediate move on quota exhaustion.** When the pinned credential drops out, the plugin reads its runtime state from CLIProxyAPI (`host.auth.list`, cached for 2 seconds). It moves the pin right away when:
  - the credential is in a cooldown longer than `max(30m, switch_after)`, which means a provider quota reset; or
  - the credential is disabled.
- **What still waits:** transient errors, auth errors and retries keep the pin. The `switch_after` grace period remains the fallback.
- **Fixed:** traffic could snap back to the exhausted credential if no requests arrived during the grace period and its quota reset in the meantime.
- **Clearer logs:** pin-move messages now say why the pin moved, e.g. `in cooldown until <time>`, `disabled`, or `unavailable for <duration>`.

Requires CLIProxyAPI 7.3.8 or newer.
