# Headless and CI Authentication

By default `entire login` opens a browser to sign in and stores tokens in the OS keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager). Machines without a usable browser or keyring — headless servers, containers, minimal VMs, CI runners — have two supported paths:

## Interactive login on a headless machine

Sign-in itself already handles this: with no interactive terminal, over SSH, or on a Linux or BSD machine with no graphical display, `entire login` switches to the device-code flow on its own and prints an approval URL you can open on any machine. `entire login --device` forces that flow explicitly. Only token *storage* needs an override — use the file-backed store:

```bash
ENTIRE_TOKEN_STORE=file entire login
```

Tokens are written with `0600` permissions to `tokens.json` in your Entire config directory (`~/.config/entire` by default). Override the location with `ENTIRE_TOKEN_STORE_PATH`. Set `ENTIRE_TOKEN_STORE=file` persistently (e.g. in your shell profile) so later commands read from the same store.

## Non-interactive automation (CI, workload identity)

Skip login and storage entirely by injecting a token per invocation:

```bash
ENTIRE_TOKEN=<login-or-sa-session-JWT> entire ...
```

`ENTIRE_TOKEN` bypasses stored credentials; the CLI derives the control-plane endpoint from the token itself. Nothing is written to disk. This is the right path for CI pipelines and service accounts.

> **Seeing `save login` / `failed to unlock correct collection` errors from `entire login`?** That's the OS keyring being unavailable — use one of the two paths above.
