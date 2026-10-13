## v0.4.0

- **Pins survive restarts.** New optional `pins_file` setting: an absolute path where the plugin saves pins whenever one is created or moves, and restores them when it loads. Without it, a restart still sends every route back to priority order. The file holds provider, model, auth ID and pin time only. A restored pin whose credential is gone moves after `switch_after`; an unreadable file is logged and replaced. Until a restored route serves its first request, the pins route reports its pin time as `last_picked_at`.
- **Docs:** corrected which CLIProxyAPI cooldowns exceed the 30-minute floor. A model the credential does not support (12 hours) and a long `Retry-After` also move the pin, which is intended.

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
