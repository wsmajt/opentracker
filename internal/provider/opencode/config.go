package opencode

import "encoding/json"

// OpenCodeConfig holds provider-specific configuration for OpenCode.
type OpenCodeConfig struct {
	Workspace string `json:"workspace"`
}

// ParseConfig unmarshals the raw JSON config into OpenCodeConfig.
func ParseConfig(raw json.RawMessage) (*OpenCodeConfig, error) {
	var cfg OpenCodeConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
