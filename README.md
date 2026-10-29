# OpenTracker

A lightweight CLI tool for tracking AI provider usage limits. It supports OpenCode, Codex, and Claude Code OAuth usage.

## Features

- **Usage tracking** - Monitor rolling, weekly, and monthly usage percentages
- **Multiple providers** - OpenCode (Go/Zen plans), Codex, and Claude Code OAuth
- **Multiple plans** - Support for different OpenCode plans (go, zen) sharing the same workspace and cookies
- **Verified setup** - Detects the OpenCode workspace from the imported authenticated browser session before saving configuration
- **Automatic cookie import** - Scans Chrome, Firefox, Zen Browser, and more for session cookies
- **Clean JSON output** - Pipe-friendly output for integration with other tools

## Quick Start

```bash
# Build
make build

# Or install directly
make install

# Log in to OpenCode (auto-imports cookies from your browser)
opentracker login opencode

# Or log in to Codex (runs 'codex login')
opentracker login codex

# Save a CLIProxyAPI management key in the system keyring
opentracker login cliproxyapi

# Claude Code OAuth (uses existing Claude Code credentials)
opentracker login claude

# Fetch usage
opentracker fetch opencode-go
opentracker fetch codex
opentracker fetch cliproxyapi
opentracker fetch claude

# Show today's local OpenCode totals
opentracker stats opencode
```

## Installation

### From source

```bash
git clone https://github.com/wsmajt/opentracker.git
cd opentracker
make build
sudo make install
```

### AUR (Arch Linux)

```bash
yay -S opentracker-cli
```

The current stable release is `1.5.0`. The AUR package `opentracker-cli` may not yet match this release.

## Usage

### Fetch usage data

```bash
# Fetch current usage (OpenCode and local Claude are cached for 90 seconds)
opentracker fetch opencode-go
opentracker fetch codex
opentracker fetch claude

# Force refresh (skip cache)
opentracker fetch opencode-go --force
opentracker fetch codex --force
opentracker fetch cliproxyapi --force
opentracker fetch claude --force

# Check version
opentracker version
```

### Local OpenCode stats

```bash
opentracker stats opencode
opentracker stats opencode --force
```

This command runs the installed `opencode` CLI with `stats --json --standalone --days 0`. It reports today's total tokens and session count as JSON, and caches the result for five minutes. Use it while the panel is open; it does not run as a background service. `--force` skips the cache.

OpenTracker does not read the OpenCode database, load account credentials, or make network requests for this command. It delegates to the official CLI, which may create or migrate its database. The output omits cost, model, project, and session history data. Install a current OpenTracker binary to use this command, and make sure `opencode` is available on `PATH`.

The `opencode` binary is resolved via `OPENCODE_BIN` first, then `PATH`, then `~/.bun/bin/opencode`, `~/.local/bin/opencode`, and `/usr/local/bin/opencode`. This matters for graphical panels, whose `PATH` is often smaller than a terminal's.

### Login

```bash
# OpenCode: automatic cookie import (scans browsers silently)
opentracker login opencode

# Codex: runs 'codex login' to authenticate
opentracker login codex

# With verbose output (shows which browsers were checked)
opentracker login opencode --verbose
```

**OpenCode** will open `https://opencode.ai/console/login` in your browser. Sign in to the correct account and press **Enter** to import cookies. Import requires an authenticated console session cookie. OpenTracker verifies the session against `/console/api/orgs` and requires a validated workspace selection before changing credentials or configuration. If verification, workspace listing, or selection fails, login reports an error and the existing account remains selected; there is no manual workspace-ID override.

OpenCode session cookies are stored in the operating system keyring; OpenTracker does not write a plaintext cookie file and does not fall back to one. Login saves a new keyring record, verifies it, and then switches the active workspace/credential selector in the config. A previous keyring entry and any legacy plaintext cookie file are removed only after the new selector and credential have been confirmed. If migration cannot complete, the existing account remains selected and old data is retained. A legacy plaintext cookie file may be removed after a successful login migration; it is never read as a fallback.

The keyring must be available and unlocked when logging in and fetching OpenCode usage. If it is locked or unavailable, unlock or configure your desktop/system keyring and retry; OpenTracker will report an error rather than saving credentials in plaintext. Keyring storage reduces accidental exposure but is not protection from malware or other code running as the same user.

OpenCode and local Claude fetch results are cached for 90 seconds. Codex, CLIProxyAPI, and combined fetches bypass the usage cache. Use `--force` to skip the cache for providers that use it. A successful OpenCode login clears both OpenCode plan caches.

**Codex** will run the `codex login` command. After authentication completes, you can fetch usage with `opentracker fetch codex`.

### CLIProxyAPI accounts

Run `opentracker login cliproxyapi` to enter the management key at a hidden terminal prompt. OpenTracker checks the key with CLIProxyAPI before replacing the saved key, then stores it in the operating system keyring. It does not put the key in the config file or command history. Login requires an interactive terminal and an available, unlocked system keyring.

For automation, set `OPENTRACKER_CLIPROXYAPI_KEY`. This environment variable takes precedence over the saved key. The default management URL is `http://127.0.0.1:8317`. Set `OPENTRACKER_CLIPROXYAPI_URL` to change it. HTTP is accepted only for loopback hosts. Remote hosts must use HTTPS. The client does not follow redirects. Set the URL in both the login and fetch environments when using a non-default endpoint. If no key is configured, `fetch cliproxyapi` reports the login command and environment variable to use.

`opentracker fetch cliproxyapi` fetches active Codex and Claude credentials exposed by CLIProxyAPI's v8 management API. `opentracker fetch codex` keeps the local Codex result and appends proxy Codex accounts. When a local and proxy Codex account have the same non-empty authoritative ChatGPT account ID, the accounts are deduplicated. If the matching proxy account has an email and the local result does not, OpenTracker copies that email to the local result. It keeps any email already present in the local result. A proxy email is personal information and a display label, not a stable account identity; matching emails alone never trigger deduplication. `fetch all` deduplicates matching Codex accounts but does not copy the proxy email onto the local result. `opentracker fetch claude` keeps the local Claude result when configured and appends proxy Claude accounts. Proxy results use provider `cliproxyapi` and include `accountProvider` to identify the upstream service. CLIProxyAPI does not provide a reliable Claude account ID, so Claude results remain separate even when emails match. Local Claude OAuth results do not include an email; a proxy Claude result may include one if CLIProxyAPI returns it.

Successful accounts go to stdout as JSON. Per-account failures go to stderr with a safe account-list ordinal, and the command exits with an error status if any account fails. A CLIProxyAPI 429 response asks you to retry later. OpenTracker does not refresh proxy tokens. `fetch all` fetches proxy accounts once and does not repeat them through the Codex or Claude fetch. It prints separate JSON arrays for provider groups, not one combined JSON document. Codex, proxy and combined results bypass cache; Claude's local rate-limit cooldown still applies.

### Codex manual reset credits

Codex usage may include `resetCredits.available` and `resetCredits.applicable` when the service supplies them. Missing fields mean the count is unknown, not zero. Both local Codex and CLIProxyAPI fetches make a bounded, best-effort read of reset-credit details. A details count takes precedence when present, including a reported zero; a failed details request does not block usage output or replace an unknown count with zero. The `credits` list contains available Codex rate-limit credits returned by the endpoint, but may be capped and incomplete when `available` is larger than the list. `expiresAt` is an RFC3339 timestamp, `null` when the service says a credit does not expire, and omitted when expiry is unknown. For example:

```json
"resetCredits": {
  "available": 2,
  "applicable": 1,
  "credits": [
    {"expiresAt": "2026-11-01T12:00:00Z"},
    {"expiresAt": null}
  ]
}
```

To reset eligible windows for an enabled Codex OAuth account through CLIProxyAPI v8, first inspect `opentracker fetch cliproxyapi` and find its `authIndex` and `accountId`. Then run `opentracker reset codex --account <authIndex>` in an interactive terminal. The prompt shows the account ID's full SHA-256 fingerprint, not the ID itself. On Linux, calculate the expected fingerprint with `printf '%s' '<accountId>' | sha256sum`. Compare it with the prompt and type `RESET` to confirm. This action may consume one reset credit; it is never run by `fetch`.

The command requires a resolvable ChatGPT account ID and a CLIProxyAPI management key. It only sends reset actions through CLIProxyAPI. Direct/local Codex resets and FedRAMP backends are not supported. After a completed operation, a separate reset requires `--new <operation-id>` with the operation ID returned by the previous result, followed by a new confirmation. OpenTracker does not retry consumption automatically. An unknown outcome or failed cooldown cleanup leaves the local journal unresolved and blocks further resets for that account. Do not retry or delete the reset state. Inspect the account and proxy, then seek manual assistance. This fail-closed protection depends on the local state journal and is not shared across machines or state directories. Safe state locking and durable storage are supported on Linux, macOS, FreeBSD, OpenBSD, NetBSD, and DragonFly BSD; other platforms refuse reset actions. The upstream API and CLIProxyAPI can change.

**Claude** reuses the access token from `~/.claude/.credentials.json` and does not start Claude Code or modify its files. Set `CLAUDE_CONFIG_DIR` or `CLAUDE_SECURESTORAGE_CONFIG_DIR` when Claude Code uses a custom config directory. You can instead set `OPENTRACKER_CLAUDE_ACCESS_TOKEN`. An explicitly set token takes precedence, and the API response determines whether it is accepted. OpenTracker does not fall back to a credentials file when the environment variable is set. macOS keychain-only credentials are not supported. The usage endpoint is private to Claude Code and may change. OpenTracker does not refresh tokens, and expired credentials require signing in with Claude Code again. A 429 response creates a credential-specific cooldown that also applies to forced fetches.

Claude output includes `fiveHour` and `sevenDay` windows, plus `sonnet` and `opus` windows when the API returns them. Each window has `usedPercent`, `resetsAt` when supplied, and `windowSeconds`. Plan metadata may appear as `plan`; `source` is `claude-oauth`.

### Example output

```json
[
  {
    "provider": "opencode-go",
    "usage": {
      "rolling": {
        "usedPercent": 35,
        "resetsAt": "2026-05-14T23:46:05Z",
        "windowMinutes": 175
      },
      "weekly": {
        "usedPercent": 38,
        "resetsAt": "2026-05-18T00:00:00Z",
        "windowMinutes": 4509
      },
      "monthly": {
        "usedPercent": 41,
        "resetsAt": "2026-05-22T19:18:53Z",
        "windowMinutes": 11442
      }
    }
  }
]
```

## Documentation

Full documentation is available in the [GitHub Wiki](https://github.com/wsmajt/opentracker/wiki):

- [Configuration](https://github.com/wsmajt/opentracker/wiki/Configuration) — Config file format, locations, and troubleshooting
- [Providers](https://github.com/wsmajt/opentracker/wiki/Providers) — Provider system overview and how to add new ones
- [OpenCode](https://github.com/wsmajt/opentracker/wiki/OpenCode) — OpenCode provider details, login, and usage
- [Codex](https://github.com/wsmajt/opentracker/wiki/Codex) — Codex/OpenAI provider details, login, and usage
- [Claude](https://github.com/wsmajt/opentracker/wiki/Claude) — Claude Code OAuth usage, credential paths, and limitations
- [CLIProxyAPI](https://github.com/wsmajt/opentracker/wiki/CLIProxyAPI) — Management API setup, proxy accounts, and security
- [Local OpenCode stats](https://github.com/wsmajt/opentracker/wiki/LocalStats) — Local token and session totals, separate from quota data

## License

MIT License — see [LICENSE](LICENSE)

## Credits
[steipete](https://github.com/steipete/CodexBar) - CodexBar
