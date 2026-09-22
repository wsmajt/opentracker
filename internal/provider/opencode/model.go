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

// ZenBilling represents OpenCode Zen (pay-as-you-go) billing data returned by
// the console billing API. Micro-cent amounts (1 USD = 1e8 micro-cents) are
// preserved as raw strings and also exposed as dollars, matching the console UI.
type ZenBilling struct {
	BillingMode           string   `json:"billingMode"`
	Mode                  string   `json:"mode"`
	BalanceMicroCents     string   `json:"balanceMicroCents"`
	BalanceDollars        float64  `json:"balanceDollars"`
	CreditLimitMicroCents *string  `json:"creditLimitMicroCents"`
	CreditLimitDollars    *float64 `json:"creditLimitDollars,omitempty"`
	AvailableMicroCents   string   `json:"availableMicroCents"`
	AvailableDollars      float64  `json:"availableDollars"`
	CanPurchaseCredits    bool     `json:"canPurchaseCredits"`
	CanEnableAutoRecharge bool     `json:"canEnableAutoRecharge"`
	CanEnrollInPrepaid    bool     `json:"canEnrollInPrepaid"`
}
