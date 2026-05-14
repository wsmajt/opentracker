package model

type Entry struct {
	UsedPercent   int    `json:"usedPercent"`
	ResetsAt      string `json:"resetsAt"`
	WindowMinutes int    `json:"windowMinutes"`
}

type Usage struct {
	Rolling *Entry `json:"rolling,omitempty"`
	Weekly  *Entry `json:"weekly,omitempty"`
	Monthly *Entry `json:"monthly,omitempty"`
}

type ProviderResult struct {
	Provider string `json:"provider"`
	Usage    Usage  `json:"usage"`
}
