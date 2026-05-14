# OpenTracker

A lightweight CLI tool for tracking AI provider usage limits. Currently supports **OpenCode** (Go plan), with a modular architecture designed for easy extension to additional providers.

## Features

- **Usage tracking** - Monitor rolling, weekly, and monthly usage percentages
- **Multiple plans** - Support for different OpenCode plans (go, zen in the future) sharing the same workspace and cookies
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

# Fetch usage
opentracker fetch opencode-go
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

# Force refresh (skip cache)
opentracker fetch opencode-go --force

# Check version
opentracker version
```

### Login

```bash
# Automatic cookie import (default — scans browsers silently)
opentracker login opencode

# With verbose output (shows which browsers were checked)
opentracker login opencode --verbose
```

This will open `https://opencode.ai/go` in your browser. After logging in, press **Enter** and OpenTracker will automatically find and save your session cookies, then detect and save your workspace ID.

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

## License

MIT License — see [LICENSE](LICENSE)
