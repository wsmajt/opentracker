package model

// ProviderResult wraps a provider's output.
type ProviderResult struct {
	Provider string      `json:"provider"`
	Usage    interface{} `json:"usage"`
}
