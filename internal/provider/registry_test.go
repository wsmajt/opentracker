package provider

import (
	"context"
	"errors"
	"testing"

	"opentracker/internal/config"
)

func resetRegistry() {
	registry = make(map[string]func(cfg *config.Config) (Provider, error))
}

func TestRegisterAndGet(t *testing.T) {
	resetRegistry()

	dummy := &mockProvider{name: "test"}
	Register("test", func(cfg *config.Config) (Provider, error) {
		return dummy, nil
	})

	p, err := Get("test", &config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "test" {
		t.Errorf("name = %q, want test", p.Name())
	}
}

func TestGet_Unknown(t *testing.T) {
	resetRegistry()

	_, err := Get("unknown", &config.Config{})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestList(t *testing.T) {
	resetRegistry()

	Register("a", func(cfg *config.Config) (Provider, error) { return nil, nil })
	Register("b", func(cfg *config.Config) (Provider, error) { return nil, nil })

	names := List()
	if len(names) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(names))
	}

	seen := make(map[string]bool)
	for _, n := range names {
		seen[n] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestAll(t *testing.T) {
	resetRegistry()

	Register("p1", func(cfg *config.Config) (Provider, error) {
		return &mockProvider{name: "p1"}, nil
	})
	Register("p2", func(cfg *config.Config) (Provider, error) {
		return &mockProvider{name: "p2"}, nil
	})

	providers, err := All(&config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
}

func TestAll_FactoryError(t *testing.T) {
	resetRegistry()

	Register("bad", func(cfg *config.Config) (Provider, error) {
		return nil, errors.New("factory failure")
	})

	_, err := All(&config.Config{})
	if err == nil {
		t.Fatal("expected error from failing factory")
	}
}

type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Fetch(ctx context.Context) (string, error) {
	_ = ctx
	return "", nil
}
func (m *mockProvider) Parse(html string) (interface{}, error) {
	_ = html
	return nil, nil
}
