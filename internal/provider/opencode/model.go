package opencode

// UsageWindow represents a single usage window.
type UsageWindow struct {
	UsedPercent   int    `json:"usedPercent"`
	ResetsAt      string `json:"resetsAt"`
	WindowMinutes int    `json:"windowMinutes"`
}

// ZenUsage represents OpenCode Zen (billing) usage data.
type ZenUsage struct {
	Rolling *UsageWindow `json:"rolling,omitempty"`
	Weekly  *UsageWindow `json:"weekly,omitempty"`
}

// GoUsage represents OpenCode Go plan usage data.
type GoUsage struct {
	Rolling *UsageWindow `json:"rolling,omitempty"`
	Weekly  *UsageWindow `json:"weekly,omitempty"`
	Monthly *UsageWindow `json:"monthly,omitempty"`
}
