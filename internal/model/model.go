package model

type Entry struct {
	UsedPercent   int    `json:"usedPercent"`
	ResetsAt      string `json:"resetsAt"`
	WindowMinutes int    `json:"windowMinutes"`
}

type Usage struct {
	Primary   *Entry `json:"primary,omitempty"`
	Secondary *Entry `json:"secondary,omitempty"`
	Tertiary  *Entry `json:"tertiary,omitempty"`
}

type ProviderResult struct {
	Provider string `json:"provider"`
	Usage    Usage  `json:"usage"`
}
