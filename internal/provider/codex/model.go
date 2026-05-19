package codex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

type UsageResponse struct {
	PlanType  string     `json:"plan_type"`
	RateLimit *RateLimit `json:"rate_limit"`
	Credits   *Credits   `json:"credits"`
}

type RateLimit struct {
	PrimaryWindow   *APIWindow `json:"primary_window"`
	SecondaryWindow *APIWindow `json:"secondary_window"`
}

type APIWindow struct {
	UsedPercent        FlexibleFloat `json:"used_percent"`
	ResetAt            FlexibleInt64 `json:"reset_at"`
	LimitWindowSeconds FlexibleInt64 `json:"limit_window_seconds"`
}

type Credits struct {
	HasCredits bool          `json:"has_credits"`
	Unlimited  bool          `json:"unlimited"`
	Balance    FlexibleFloat `json:"balance"`
}

type RateWindow struct {
	UsedPercent      int     `json:"usedPercent"`
	RemainingPercent int     `json:"remainingPercent"`
	WindowSeconds    *int64  `json:"windowSeconds,omitempty"`
	ResetsAt         *string `json:"resetsAt,omitempty"`
}

type UsageSnapshot struct {
	AccountEmail     string      `json:"accountEmail,omitempty"`
	Plan             string      `json:"plan,omitempty"`
	Primary          *RateWindow `json:"primary,omitempty"`
	Secondary        *RateWindow `json:"secondary,omitempty"`
	CreditsRemaining *float64    `json:"creditsRemaining,omitempty"`
	UpdatedAt        string      `json:"updatedAt"`
	Source           string      `json:"source"`
}

type fetchEnvelope struct {
	UsageResponse UsageResponse `json:"usageResponse"`
	AccountEmail  string        `json:"accountEmail,omitempty"`
}

type FlexibleFloat struct {
	Value float64
	Set   bool
}

func (f *FlexibleFloat) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var number float64
	if err := json.Unmarshal(data, &number); err == nil {
		f.Value = number
		f.Set = true
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return fmt.Errorf("invalid number %q: %w", text, err)
	}
	f.Value = parsed
	f.Set = true
	return nil
}

func (f FlexibleFloat) MarshalJSON() ([]byte, error) {
	if !f.Set {
		return []byte("null"), nil
	}
	return json.Marshal(f.Value)
}

type FlexibleInt64 struct {
	Value int64
	Set   bool
}

func (f *FlexibleInt64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var number int64
	if err := json.Unmarshal(data, &number); err == nil {
		f.Value = number
		f.Set = true
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid integer %q: %w", text, err)
	}
	f.Value = parsed
	f.Set = true
	return nil
}

func (f FlexibleInt64) MarshalJSON() ([]byte, error) {
	if !f.Set {
		return []byte("null"), nil
	}
	return json.Marshal(f.Value)
}

func mapUsageResponse(api UsageResponse, email string, now time.Time) UsageSnapshot {
	snapshot := UsageSnapshot{
		AccountEmail: email,
		Plan:         displayPlan(api.PlanType),
		UpdatedAt:    now.UTC().Format(time.RFC3339),
		Source:       "codex-oauth",
	}

	if api.RateLimit != nil {
		snapshot.Primary = mapWindow(api.RateLimit.PrimaryWindow)
		snapshot.Secondary = mapWindow(api.RateLimit.SecondaryWindow)
	}
	if api.Credits != nil && api.Credits.Balance.Set {
		balance := api.Credits.Balance.Value
		snapshot.CreditsRemaining = &balance
	}

	return snapshot
}

func mapWindow(window *APIWindow) *RateWindow {
	if window == nil {
		return nil
	}
	if !window.UsedPercent.Set {
		return nil
	}
	used := clampPercent(window.UsedPercent.Value)
	result := &RateWindow{
		UsedPercent:      used,
		RemainingPercent: 100 - used,
	}
	if window.LimitWindowSeconds.Set {
		seconds := window.LimitWindowSeconds.Value
		result.WindowSeconds = &seconds
	}
	if window.ResetAt.Set && window.ResetAt.Value > 0 {
		reset := time.Unix(window.ResetAt.Value, 0).UTC().Format(time.RFC3339)
		result.ResetsAt = &reset
	}
	return result
}

func clampPercent(value float64) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return int(value)
}

func displayPlan(plan string) string {
	if plan == "" {
		return ""
	}
	return plan
}
