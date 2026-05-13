# Providers

## Available providers

| Provider | Description | Config key |
|----------|-------------|------------|
| `opencode-go` | OpenCode Go plan | `opencode` (shared) |
| `opencode-zen` | OpenCode Zen plan | `opencode` (shared) |

All OpenCode plans share the same workspace configuration and cookies.

## How to add a new provider

To add a new provider (e.g., for a different AI service), follow these steps:

### 1. Create a new package

Create a new directory under `internal/provider/`:

```
internal/provider/
├── myprovider/
│   └── myprovider.go
```

### 2. Implement the Provider interface

Your provider must implement the `provider.Provider` interface:

```go
package myprovider

import (
    "context"
    "opentracker/internal/model"
    "opentracker/internal/provider"
)

type MyProvider struct {
    // your fields
}

func (m *MyProvider) Name() string {
    return "myprovider"
}

func (m *MyProvider) Fetch(ctx context.Context) (string, error) {
    // Fetch raw data (HTML, JSON, etc.)
    return "...", nil
}

func (m *MyProvider) Parse(data string) (model.Usage, error) {
    // Parse raw data into model.Usage
    return model.Usage{}, nil
}
```

### 3. Register the provider

In your package's `init()` function, register the provider:

```go
func init() {
    provider.Register("myprovider", func(cfg *config.Config) (provider.Provider, error) {
        return New(cfg)
    })
}
```

### 4. Import in main.go

Add a blank import in `main.go` so the `init()` function runs:

```go
import (
    _ "opentracker/internal/provider/myprovider"
)
```

### 5. Rebuild

```bash
go build ./...
```

Your new provider is now available via `opentracker fetch myprovider`.

## Provider architecture

```
internal/provider/
├── provider.go          # Provider interface definition
├── registry.go          # Provider registration and lookup
├── opencode/            # Shared OpenCode code (config, parser)
│   ├── config.go
│   └── parse.go
└── opencodego/          # OpenCode Go plan implementation
    └── opencodego.go
```

### Provider interface

```go
type Provider interface {
    Name() string
    Fetch(ctx context.Context) (string, error)
    Parse(html string) (model.Usage, error)
}
```

- **Name** — Returns the provider identifier used in CLI commands
- **Fetch** — Downloads raw data from the provider's API/website
- **Parse** — Converts raw data into structured `model.Usage`

### Shared packages

If multiple providers share common code (like OpenCode plans), extract shared logic into a separate package (e.g., `internal/provider/opencode/`).

## Tips

- Use `internal/fetcher` for HTTP requests with cookie support
- Use `internal/cache` for caching results
- Use `internal/output` for consistent JSON formatting
- Follow the existing code style and error handling patterns
