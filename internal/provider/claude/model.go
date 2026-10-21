package claude

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type UsageSnapshot struct {
	Plan      string      `json:"plan,omitempty"`
	FiveHour  *RateWindow `json:"fiveHour,omitempty"`
	SevenDay  *RateWindow `json:"sevenDay,omitempty"`
	Sonnet    *RateWindow `json:"sonnet,omitempty"`
	Opus      *RateWindow `json:"opus,omitempty"`
	UpdatedAt string      `json:"updatedAt"`
	Source    string      `json:"source"`
}

type RateWindow struct {
	UsedPercent   float64 `json:"usedPercent"`
	ResetsAt      *string `json:"resetsAt,omitempty"`
	WindowSeconds int64   `json:"windowSeconds"`
}

type apiUsage struct {
	FiveHour *apiWindow `json:"five_hour"`
	SevenDay *apiWindow `json:"seven_day"`
	Sonnet   *apiWindow `json:"seven_day_sonnet"`
	Opus     *apiWindow `json:"seven_day_opus"`
}

type apiWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

func parseUsage(data string, plan string, now time.Time) (UsageSnapshot, error) {
	var response apiUsage
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return UsageSnapshot{}, fmt.Errorf("cannot decode Claude usage response")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &object); err != nil || object == nil {
		return UsageSnapshot{}, fmt.Errorf("claude usage response must be a JSON object")
	}
	snapshot := UsageSnapshot{Plan: plan, UpdatedAt: now.UTC().Format(time.RFC3339), Source: "claude-oauth"}
	snapshot.FiveHour = mapWindow(response.FiveHour, 18000)
	snapshot.SevenDay = mapWindow(response.SevenDay, 604800)
	snapshot.Sonnet = mapWindow(response.Sonnet, 604800)
	snapshot.Opus = mapWindow(response.Opus, 604800)
	return snapshot, nil
}

func mapWindow(value *apiWindow, seconds int64) *RateWindow {
	if value == nil || value.Utilization == nil || math.IsNaN(*value.Utilization) || math.IsInf(*value.Utilization, 0) || *value.Utilization < 0 || *value.Utilization > 100 {
		return nil
	}
	window := &RateWindow{UsedPercent: *value.Utilization, WindowSeconds: seconds}
	if value.ResetsAt != nil {
		if parsed, err := time.Parse(time.RFC3339, *value.ResetsAt); err == nil {
			reset := parsed.UTC().Format(time.RFC3339)
			window.ResetsAt = &reset
		}
	}
	return window
}
