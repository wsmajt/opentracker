package provider

import (
	"context"
)

// Provider defines the interface for fetching and parsing usage data.
type Provider interface {
	Name() string
	Fetch(ctx context.Context) (string, error)
	Parse(html string) (interface{}, error)
}

// Result wraps a provider's output.
type Result struct {
	Provider string
	Usage    interface{}
	Error    error
}
