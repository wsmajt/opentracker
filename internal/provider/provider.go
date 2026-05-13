package provider

import (
	"context"

	"opentracker/internal/model"
)

// Provider defines the interface for fetching and parsing usage data.
type Provider interface {
	Name() string
	Fetch(ctx context.Context) (string, error)
	Parse(html string) (model.Usage, error)
}

// Result wraps a provider's output.
type Result struct {
	Provider string
	Usage    model.Usage
	Error    error
}
