# Configuration

OpenTracker stores its configuration and cache in standard XDG directories.

## Config file

**Location:** `~/.config/opentracker/config.json`

**Format:**
```json
{
  "providers": {
    "opencode": {
      "workspace": "wrk_01KPV9Z3N4E374YV6TXMN90EA5"
    }
  }
}
```

The `opencode` key is shared across all OpenCode plans (`opencode-go`, `opencode-zen`, etc.). This means you only need to configure your workspace once.

## Cache

**Location:** `~/.cache/opentracker/`

Each provider has its own cache file (e.g., `opencode-go.json`). Cache TTL is **90 seconds** by default. Use `--force` to bypass cache.

## Cookie file

**Location:** `~/.config/opentracker/opencode-cookies.txt`

Cookies must be in **Netscape HTTP Cookie File** format. You can export them using browser extensions like:
- [Export Cookies](https://addons.mozilla.org/en-US/firefox/addon/export-cookies-txt/) for Firefox

## First-time setup

When you run `opentracker fetch opencode-go` for the first time and no configuration exists, the CLI will interactively prompt you for your workspace ID:

```
Provider "opencode-go" is not configured.
Workspace ID: wrk_...
Config saved.
```

The workspace ID can be found in your OpenCode dashboard URL: `https://opencode.ai/workspace/WORKSPACE_ID/go`

## Troubleshooting

### "session expired or no usage data found"

This means your cookies are invalid or expired. To fix:

1. Run `opentracker login opencode-go`
2. Log in to OpenCode in your browser
3. Export fresh cookies to `~/.config/opentracker/opencode-cookies.txt`
4. Run `opentracker fetch opencode-go --force`

### "cannot open cookie file"

Make sure the cookie file exists and is readable:

```bash
ls -la ~/.config/opentracker/opencode-cookies.txt
```

If it doesn't exist, export cookies from your browser after logging in.

### "opencode workspace not set"

Your config file is missing the workspace. Delete the config and let the CLI prompt you again:

```bash
rm ~/.config/opentracker/config.json
opentracker fetch opencode-go
```

## Environment variables

There are no required environment variables. All configuration is stored in the config file.
