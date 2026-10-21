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

# Claude Code OAuth (uses existing Claude Code credentials)
opentracker login claude

# Fetch usage
opentracker fetch opencode-go
opentracker fetch codex
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

The current test release is `1.5.0-beta.1`. The AUR package `opentracker-cli` is at `1.5.0beta1-1`, replacing its stable version during beta testing. The planned stable release is `1.5.0`.

## Usage

### Fetch usage data

```bash
# Fetch current usage (cached for 90 seconds)
opentracker fetch opencode-go
opentracker fetch codex
opentracker fetch claude

# Force refresh (skip cache)
opentracker fetch opencode-go --force
opentracker fetch codex --force
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

Fetch results are cached for 90 seconds. Use `opentracker fetch opencode-go --force` (or `opencode-zen --force`) to bypass the cache when you need an immediate fresh result. A successful login clears both OpenCode plan caches.

**Codex** will run the `codex login` command. After authentication completes, you can fetch usage with `opentracker fetch codex`.

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
- [Local OpenCode stats](https://github.com/wsmajt/opentracker/wiki/LocalStats) — Local token and session totals, separate from quota data

## License

MIT License — see [LICENSE](LICENSE)

## Credits
[steipete](https://github.com/steipete/CodexBar) - CodexBar
