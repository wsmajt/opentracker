# OpenTracker

A lightweight CLI tool for tracking AI provider usage limits. Currently supports **OpenCode** (Go plan), with a modular architecture designed for easy extension to additional providers.

## Features

- **Usage tracking** - Monitor rolling, weekly, and monthly usage percentages
- **Multiple plans** - Support for different OpenCode plans (go, zen in the future) sharing the same workspace and cookies
- **Interactive setup** - Prompts for workspace ID on first use, saves configuration automatically
- **Session handling** - Uses exported Netscape-format cookies for authentication
- **Clean JSON output** - Pipe-friendly output for integration with other tools

## Quick Start

```bash
# Build
make build

# Or install directly
make install

# Log in to OpenCode (opens browser)
opentracker login opencode

# Export cookies in Netscape format to ~/.config/opentracker/opencode-cookies.txt
# Then fetch usage
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
opentracker login opencode
```

This will open `https://opencode.ai/go` in your browser. After logging in, export your cookies in **Netscape format** to `~/.config/opentracker/opencode-cookies.txt`.

### Example output

```json
[
  {
    "provider": "opencode-go",
    "usage": {
      "primary": {
        "usedPercent": 10,
        "resetsAt": "2026-05-13T23:47:22Z",
        "windowMinutes": 201
      },
      "secondary": {
        "usedPercent": 7,
        "resetsAt": "2026-05-17T23:26:22Z",
        "windowMinutes": 5940
      },
      "tertiary": {
        "usedPercent": 25,
        "resetsAt": "2026-05-22T18:26:22Z",
        "windowMinutes": 12840
      }
    }
  }
]
```

## Documentation

- [Configuration](docs/CONFIGURATION.md) — Config file format, locations, and troubleshooting
- [Providers](docs/PROVIDERS.md) — Available providers and how to add new ones

## License

MIT License — see [LICENSE](LICENSE)
