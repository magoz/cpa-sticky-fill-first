Sticky fill-first credential routing for CLIProxyAPI. Each provider and model route stays on the credential that last served it. The pin moves only after that credential has been unavailable for `switch_after` (default `5m`), so traffic no longer returns to the top-priority account as soon as its quota window resets.

Requires CLIProxyAPI 7.3.8 or newer. See the README for configuration and caveats.
