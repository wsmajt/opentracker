# OpenTracker

A lightweight CLI tool for tracking AI provider usage limits. Currently supports **OpenCode** (Go plan) and **Codex** (OpenAI/Codex CLI), with a modular architecture designed for easy extension to additional providers.

## Features

- **Usage tracking** - Monitor rolling, weekly, and monthly usage percentages
- **Multiple providers** - OpenCode (Go/Zen plans) and Codex (OpenAI usage via Codex CLI auth)
- **Multiple plans** - Support for different OpenCode plans (go, zen) sharing the same workspace and cookies
- **Interactive setup** - Prompts for workspace ID on first use, saves configuration automatically
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

# Fetch usage
opentracker fetch opencode-go
opentracker fetch codex
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

## Usage

### Fetch usage data

```bash
# Fetch current usage (cached for 90 seconds)
opentracker fetch opencode-go
opentracker fetch codex

# Force refresh (skip cache)
opentracker fetch opencode-go --force
opentracker fetch codex --force

# Check version
opentracker version
```

### Login

```bash
# OpenCode: automatic cookie import (scans browsers silently)
opentracker login opencode

# Codex: runs 'codex login' to authenticate
opentracker login codex

# With verbose output (shows which browsers were checked)
opentracker login opencode --verbose
```

**OpenCode** will open `https://opencode.ai/console/login` in your browser. After logging in, press **Enter** and OpenTracker will automatically find and save your session cookies, then detect and save your workspace ID.

**Codex** will run the `codex login` command. After authentication completes, you can fetch usage with `opentracker fetch codex`.

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

## License

MIT License — see [LICENSE](LICENSE)

## Credits
[steipete](https://github.com/steipete/CodexBar) - CodexBar
