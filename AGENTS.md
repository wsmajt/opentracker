# AGENTS.md

## Project overview

OpenTracker is a lightweight Go CLI for tracking AI provider usage limits. It currently targets OpenCode usage plans (`opencode-go`, `opencode-zen`) and Codex/OpenAI usage (`codex`), and is structured so more providers can be added later.

The CLI logs in by importing browser session cookies, detects the user's OpenCode workspace, fetches usage data, caches recent responses, and prints pipe-friendly JSON.

## Tech stack

- Language: Go (`go 1.26.3` in `go.mod`)
- CLI framework: `github.com/spf13/cobra`
- Browser cookie discovery: `github.com/browserutils/kooky`
- Build/test tooling: `Makefile`, `go test`, `golangci-lint` in CI
- Packaging: `PKGBUILD` and `.SRCINFO` for Arch/AUR

## Key commands

Use these from the repository root:

```bash
make build      # build ./opentracker with version ldflags
make install    # install binary to $(PREFIX)/bin, default /usr/bin
make test       # run go test ./...
make coverage   # write coverage.out and print coverage summary
make clean      # remove ./opentracker
```

CLI examples:

```bash
opentracker login opencode
opentracker login opencode --verbose
opentracker login codex
opentracker fetch opencode-go
opentracker fetch opencode-go --force
opentracker fetch codex
opentracker fetch codex --force
opentracker version
```

## Repository layout

- `main.go` - CLI entrypoint. Imports provider packages for their registration side effects, then starts the Cobra root command.
- `cmd/` - Cobra commands (`root`, `fetch`, `login`, `version`) and CLI flag wiring.
- `internal/app/` - High-level orchestration for config loading, provider lookup, cache checks, fetch execution, and output.
- `internal/config/` - JSON config load/save logic. Runtime config is stored in `~/.config/opentracker/config.json`.
- `internal/cache/` - JSON file cache in `~/.cache/opentracker`; fetch results are cached briefly unless `--force` is used.
- `internal/provider/` - Provider interfaces, registry, lookup, and shared provider concepts.
- `internal/provider/opencode/` - OpenCode implementation: login flow, workspace detection, fetch/parsing logic, and provider registration.
- `internal/provider/codex/` - Codex implementation: OAuth token loading (`~/.codex/auth.json` or env), API fetch, usage mapping, and provider registration.
- `internal/browsercookies/` - Browser cookie import/export helpers and browser-specific fallback behavior.
- `internal/fetcher/` - Cookie-backed HTTP client and Netscape cookie loader.
- `internal/output/` - Pretty JSON output helpers.
- `.github/workflows/ci.yml` - CI runs linting and tests.
- `.golangci.yml` - Linter configuration.
- `README.md` - User-facing overview and examples.

## Runtime data flow

Typical fetch path:

1. `main.go` starts the Cobra CLI.
2. A command in `cmd/` calls into `internal/app.App`.
3. The app loads config from `~/.config/opentracker/config.json`.
4. Unless `--force` is set, the app checks `~/.cache/opentracker` for a fresh cached result.
5. The provider is resolved through `internal/provider` registry.
6. The provider (OpenCode or Codex) uses saved auth to request provider pages or APIs.
7. Provider-specific parsing extracts usage data (OpenCode: rolling/weekly/monthly; Codex: primary/secondary windows + credits).
8. The app writes the result to cache and prints formatted JSON to stdout.

Typical login path:

1. `opentracker login opencode` opens or instructs the user to open OpenCode.
2. The user authenticates in a browser.
3. OpenTracker scans supported browsers for OpenCode cookies.
4. Cookies are saved for later fetches.
5. Workspace ID is detected and saved in config.

## Provider model

Providers are registered through package initialization. Importing a provider package makes it available in the registry. OpenCode plan variants share a single `opencode` config key because they use the same workspace and cookies. Codex uses `~/.codex/auth.json` or `OPENTRACKER_CODEX_ACCESS_TOKEN` env and does not store config in `opentracker/config.json`.

When adding a provider:

- Add an implementation under `internal/provider/<name>/`.
- Register it with the provider registry from an `init()` function.
- Ensure `main.go` imports the package for side effects.
- Keep provider-specific config serializable as JSON.
- Add tests for parsing, registry behavior, and error handling where possible.

## Output and behavior conventions

- Prefer JSON-only stdout for command results so output remains scriptable.
- Send human-facing diagnostics/errors to stderr when practical.
- Pretty-print JSON and keep HTML escaping disabled in JSON output.
- Preserve existing CLI names and flags unless intentionally changing the public interface.
- Keep provider parsing isolated from CLI command code.
- Avoid storing secrets or runtime cookies in the repository.

## Testing and verification

Before finishing code changes, run at least:

```bash
make test
```

For changes touching lint-sensitive code or CI behavior, also run the configured linter if available locally:

```bash
golangci-lint run
```

For user-visible fetch/login changes, verify both cached and forced fetch paths when possible.

## Known gotchas

- OpenCode parsing depends on the current OpenCode HTML/embedded JavaScript structure and may break if the site changes.
- `fetch all` may print per-provider results rather than one clean aggregated result.
- Provider ordering can be nondeterministic if it depends on Go map iteration.
- Runtime config and cookie/cache files are stored under the user's home directory, not in this repo.
- Build artifacts such as `./opentracker` and `coverage.out` may appear locally; do not treat them as source files.
- `.gitignore` currently ignores only `/opentracker`.

## Agent guidance

- Start by reading `README.md`, `cmd/`, and `internal/app/` for behavior-level changes.
- For provider changes, inspect `internal/provider/registry.go` and the relevant provider package first.
- For OpenCode issues, focus on `internal/provider/opencode/`, `internal/browsercookies/`, and `internal/fetcher/`.
- For Codex issues, focus on `internal/provider/codex/` and `cmd/login.go`.
- Prefer small, provider-scoped changes over broad CLI rewrites.
- Do not commit generated binaries, coverage output, local cookies, local config, or cache files.
