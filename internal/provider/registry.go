package provider

import (
	"fmt"

	"opentracker/internal/config"
)

var registry = map[string]func(cfg *config.Config) (Provider, error){}

// Register adds a provider factory to the registry.
func Register(name string, factory func(cfg *config.Config) (Provider, error)) {
	registry[name] = factory
}

// Get returns a provider instance by name.
func Get(name string, cfg *config.Config) (Provider, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return factory(cfg)
}

// List returns all registered provider names.
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// All returns instances of all registered providers.
func All(cfg *config.Config) ([]Provider, error) {
	providers := make([]Provider, 0, len(registry))
	for _, factory := range registry {
		p, err := factory(cfg)
		if err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, nil
}
