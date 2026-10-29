package codex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type UsageResponse struct {
	PlanType              string           `json:"plan_type"`
	RateLimit             *RateLimit       `json:"rate_limit"`
	Credits               *Credits         `json:"credits"`
	RateLimitResetCredits *APIResetCredits `json:"rate_limit_reset_credits"`
}

type APIResetCredits struct {
	AvailableCount           *FlexibleInt64 `json:"available_count"`
	ApplicableAvailableCount *FlexibleInt64 `json:"applicable_available_count"`
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
	AccountEmail     string        `json:"accountEmail,omitempty"`
	Plan             string        `json:"plan,omitempty"`
	Primary          *RateWindow   `json:"primary,omitempty"`
	Secondary        *RateWindow   `json:"secondary,omitempty"`
	CreditsRemaining *float64      `json:"creditsRemaining,omitempty"`
	ResetCredits     *ResetCredits `json:"resetCredits,omitempty"`
	UpdatedAt        string        `json:"updatedAt"`
	Source           string        `json:"source"`
}

type ResetCredits struct {
	Available  *int64               `json:"available,omitempty"`
	Applicable *int64               `json:"applicable,omitempty"`
	Credits    []ResetCreditDetails `json:"credits,omitempty"`
}

func (credits ResetCredits) MarshalJSON() ([]byte, error) {
	value := map[string]any{}
	if credits.Available != nil {
		value["available"] = credits.Available
	}
	if credits.Applicable != nil {
		value["applicable"] = credits.Applicable
	}
	if credits.Credits != nil {
		value["credits"] = credits.Credits
	}
	return json.Marshal(value)
}

type ResetCreditDetails struct {
	ExpiresAt   *string `json:"expiresAt,omitempty"`
	ExpiryKnown bool    `json:"-"`
}

func (credit ResetCreditDetails) MarshalJSON() ([]byte, error) {
	value := map[string]any{}
	if credit.ExpiryKnown {
		value["expiresAt"] = credit.ExpiresAt
	}
	return json.Marshal(value)
}

func (credit *ResetCreditDetails) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	value, present := fields["expiresAt"]
	credit.ExpiresAt = nil
	credit.ExpiryKnown = present
	if !present || strings.TrimSpace(string(value)) == "null" {
		return nil
	}
	var expiresAt string
	if err := json.Unmarshal(value, &expiresAt); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return err
	}
	canonical := parsed.UTC().Format(time.RFC3339Nano)
	credit.ExpiresAt = &canonical
	return nil
}

// ParseResetCreditsDetails reads the Codex reset-credit details response.
// Invalid optional fields are ignored so they cannot invalidate usage data.
func ParseResetCreditsDetails(data []byte) *ResetCredits {
	var raw struct {
		AvailableCount json.RawMessage `json:"available_count"`
		Credits        json.RawMessage `json:"credits"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	result := &ResetCredits{}
	var available FlexibleInt64
	if len(raw.AvailableCount) > 0 && json.Unmarshal(raw.AvailableCount, &available) == nil && available.Set {
		if available.Value >= 0 {
			result.Available = &available.Value
		}
	}
	var entries []struct {
		ResetType string          `json:"reset_type"`
		Status    string          `json:"status"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if len(raw.Credits) > 0 && json.Unmarshal(raw.Credits, &entries) == nil {
		if entries != nil {
			result.Credits = make([]ResetCreditDetails, 0, len(entries))
		}
		for _, entry := range entries {
			if entry.ResetType != "codex_rate_limits" || entry.Status != "available" {
				continue
			}
			credit := ResetCreditDetails{}
			if len(entry.ExpiresAt) > 0 {
				if strings.TrimSpace(string(entry.ExpiresAt)) == "null" {
					credit.ExpiryKnown = true
				} else {
					var value string
					if json.Unmarshal(entry.ExpiresAt, &value) != nil {
						result.Credits = append(result.Credits, credit)
						continue
					}
					parsed, err := time.Parse(time.RFC3339, value)
					if err != nil {
						result.Credits = append(result.Credits, credit)
						continue
					}
					canonical := parsed.UTC().Format(time.RFC3339Nano)
					credit.ExpiresAt = &canonical
					credit.ExpiryKnown = true
				}
			}
			result.Credits = append(result.Credits, credit)
		}
	}
	if result.Available == nil && result.Credits == nil {
		return nil
	}
	return result
}

type fetchEnvelope struct {
	UsageResponse       UsageResponse `json:"usageResponse"`
	AccountEmail        string        `json:"accountEmail,omitempty"`
	ResetCreditsDetails *ResetCredits `json:"resetCreditsDetails,omitempty"`
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
	if api.RateLimitResetCredits != nil {
		resetCredits := &ResetCredits{}
		if api.RateLimitResetCredits.AvailableCount != nil && api.RateLimitResetCredits.AvailableCount.Set {
			available := api.RateLimitResetCredits.AvailableCount.Value
			if available >= 0 {
				resetCredits.Available = &available
			}
		}
		if api.RateLimitResetCredits.ApplicableAvailableCount != nil && api.RateLimitResetCredits.ApplicableAvailableCount.Set {
			applicable := api.RateLimitResetCredits.ApplicableAvailableCount.Value
			if applicable >= 0 {
				resetCredits.Applicable = &applicable
			}
		}
		if resetCredits.Available != nil || resetCredits.Applicable != nil {
			snapshot.ResetCredits = resetCredits
		}
	}

	return snapshot
}

// MapUsageResponse converts an upstream usage payload to the public snapshot.
func MapUsageResponse(api UsageResponse, email string, now time.Time) UsageSnapshot {
	return mapUsageResponse(api, email, now)
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
