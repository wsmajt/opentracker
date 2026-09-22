package opencode

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// microCents is a micro-cent amount (1 USD = 1e8 micro-cents) serialized as a
// JSON string or number.
type microCents struct {
	raw string
}

// UnmarshalJSON accepts both quoted and unquoted amounts.
func (m *microCents) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		m.raw = ""
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		m.raw = asString
		return nil
	}
	var asNumber json.Number
	if err := json.Unmarshal(data, &asNumber); err == nil {
		m.raw = asNumber.String()
		return nil
	}
	return fmt.Errorf("invalid micro-cents amount %s", text)
}

// bigInt returns the parsed amount or zero when missing or invalid.
func (m microCents) bigInt() *big.Int {
	n := new(big.Int)
	if m.raw == "" {
		return n
	}
	if _, ok := n.SetString(m.raw, 10); !ok {
		return new(big.Int)
	}
	return n
}

// dollarsFromMicroCents converts micro-cents to dollars rounded to whole cents.
func dollarsFromMicroCents(m microCents) float64 {
	value := new(big.Rat).SetInt(m.bigInt())
	value.Quo(value, big.NewRat(100_000_000, 1))
	dollars, _ := value.Float64()
	return math.Round(dollars*100) / 100
}

// usedPercentOf converts used/limit micro-cents into a percent, mirroring the
// console UI (round half up).
func usedPercentOf(used, limit microCents) int {
	l := limit.bigInt()
	if l.Sign() <= 0 {
		return 0
	}
	num := new(big.Int).Mul(used.bigInt(), big.NewInt(200))
	num.Add(num, l)
	den := new(big.Int).Mul(l, big.NewInt(2))
	percent := new(big.Int).Div(num, den)
	max := big.NewInt(1_000_000)
	if percent.Cmp(max) > 0 {
		return int(max.Int64())
	}
	return int(percent.Int64())
}

// windowUntil formats a reset timestamp and the remaining minutes until reset
// (0 when the timestamp is absent or already past).
func windowUntil(resetsAt *time.Time) (string, int) {
	if resetsAt == nil {
		return "", 0
	}
	remaining := time.Until(*resetsAt)
	if remaining < 0 {
		remaining = 0
	}
	return resetsAt.UTC().Format(time.RFC3339), int(remaining / time.Minute)
}

// goStatusResponse mirrors the console Go status API payload
// (GET /console/api/go/status).
type goStatusResponse struct {
	Access *goAccessJSON `json:"access"`
}

type goAccessJSON struct {
	StartsAt *time.Time   `json:"startsAt"`
	EndsAt   *time.Time   `json:"endsAt"`
	Meters   goMetersJSON `json:"meters"`
}

type goMetersJSON struct {
	FiveHour goMeterJSON `json:"fiveHour"`
	Week     goMeterJSON `json:"week"`
	Month    goMeterJSON `json:"month"`
}

type goMeterJSON struct {
	StartsAt        *time.Time `json:"startsAt"`
	ResetsAt        *time.Time `json:"resetsAt"`
	LimitMicroCents microCents `json:"limitMicroCents"`
	UsedMicroCents  microCents `json:"usedMicroCents"`
}

// ParseGoStatusJSON extracts usage data from the console Go status API payload.
// Rolling and weekly windows reset at their own resetsAt timestamps; the monthly
// window resets at the end of the paid period (access.endsAt), matching the
// console UI.
func ParseGoStatusJSON(payload string) (GoUsage, error) {
	var status goStatusResponse
	if err := json.Unmarshal([]byte(payload), &status); err != nil {
		return GoUsage{}, fmt.Errorf("invalid go status JSON: %w", err)
	}
	if status.Access == nil {
		return GoUsage{}, fmt.Errorf("no usage meters found in go status response")
	}
	meters := status.Access.Meters
	return GoUsage{
		Rolling: usageWindowFromMeter(meters.FiveHour, meters.FiveHour.ResetsAt),
		Weekly:  usageWindowFromMeter(meters.Week, meters.Week.ResetsAt),
		Monthly: usageWindowFromMeter(meters.Month, status.Access.EndsAt),
	}, nil
}

func usageWindowFromMeter(meter goMeterJSON, resetsAt *time.Time) *UsageWindow {
	resets, minutes := windowUntil(resetsAt)
	return &UsageWindow{
		UsedPercent:   usedPercentOf(meter.UsedMicroCents, meter.LimitMicroCents),
		ResetsAt:      resets,
		WindowMinutes: minutes,
	}
}

// billingStatusResponse mirrors the console billing status API payload
// (GET /console/api/billing/status).
type billingStatusResponse struct {
	BillingMode           string      `json:"billingMode"`
	Mode                  string      `json:"mode"`
	BalanceMicroCents     microCents  `json:"balanceMicroCents"`
	CreditLimitMicroCents *microCents `json:"creditLimitMicroCents"`
	AvailableMicroCents   microCents  `json:"availableMicroCents"`
	CanPurchaseCredits    bool        `json:"canPurchaseCredits"`
	CanEnableAutoRecharge bool        `json:"canEnableAutoRecharge"`
	CanEnrollInPrepaid    bool        `json:"canEnrollInPrepaid"`
}

// ParseZenBillingJSON extracts billing data from the console billing status
// API payload.
func ParseZenBillingJSON(payload string) (ZenBilling, error) {
	var status billingStatusResponse
	if err := json.Unmarshal([]byte(payload), &status); err != nil {
		return ZenBilling{}, fmt.Errorf("invalid billing status JSON: %w", err)
	}
	if status.BillingMode == "" && status.Mode == "" {
		return ZenBilling{}, fmt.Errorf("no billing data found in billing status response")
	}

	result := ZenBilling{
		BillingMode:           status.BillingMode,
		Mode:                  status.Mode,
		BalanceMicroCents:     status.BalanceMicroCents.raw,
		BalanceDollars:        dollarsFromMicroCents(status.BalanceMicroCents),
		AvailableMicroCents:   status.AvailableMicroCents.raw,
		AvailableDollars:      dollarsFromMicroCents(status.AvailableMicroCents),
		CanPurchaseCredits:    status.CanPurchaseCredits,
		CanEnableAutoRecharge: status.CanEnableAutoRecharge,
		CanEnrollInPrepaid:    status.CanEnrollInPrepaid,
	}
	if status.CreditLimitMicroCents != nil {
		raw := status.CreditLimitMicroCents.raw
		dollars := dollarsFromMicroCents(*status.CreditLimitMicroCents)
		result.CreditLimitMicroCents = &raw
		result.CreditLimitDollars = &dollars
	}
	return result, nil
}

// ParseHTML extracts usage data from OpenCode Go HTML.
// It uses a two-phase approach: first parses HTML structure, then
// looks for embedded JS with exact resetInSec values.
func ParseHTML(html string) (GoUsage, error) {
	// First try to extract exact resetInSec from embedded JS. Keep the
	// original HTML because the app embeds usage data inside script tags.
	jsData := extractJSEmbeddedData(html)

	// Remove HTML comments
	html = regexp.MustCompile(`<!--.*?-->`).ReplaceAllString(html, "")

	// Phase 1: Parse HTML structure
	usage, err := parseHTMLStructure(html)
	if err != nil {
		if jsData != nil {
			usage = usageFromJSEmbeddedData(jsData)
			return usage, nil
		}
		return GoUsage{}, err
	}

	// Phase 2: Try to extract exact resetInSec from embedded JS
	if jsData != nil {
		applyJSEmbeddedData(usage, jsData)
	}

	return usage, nil
}

func parseHTMLStructure(html string) (GoUsage, error) {
	parts := regexp.MustCompile(`<div\b[^>]*\bdata-slot="usage-item"[^>]*>`).Split(html, -1)
	if len(parts) < 2 {
		return GoUsage{}, fmt.Errorf("no usage items found")
	}

	var entries = make(map[string]*UsageWindow)

	for _, part := range parts[1:] {
		endIdx := regexp.MustCompile(`<div\b[^>]*\bdata-slot="usage-item"[^>]*>`).FindStringIndex(part)
		if endIdx != nil {
			part = part[:endIdx[0]]
		}

		labelMatch := regexp.MustCompile(`<span\b[^>]*\bdata-slot="usage-label"[^>]*>(.*?)</span>`).FindStringSubmatch(part)
		progressMatch := regexp.MustCompile(`<div\b[^>]*\bdata-slot="progress-bar"[^>]*\bstyle="width:\s*(\d+)%?"[^>]*>`).FindStringSubmatch(part)
		valueMatch := regexp.MustCompile(`<span\b[^>]*\bdata-slot="usage-value"[^>]*>(.*?)</span>`).FindStringSubmatch(part)
		resetMatch := regexp.MustCompile(`<span\b[^>]*\bdata-slot="reset-time"[^>]*>(.*?)</span>`).FindStringSubmatch(part)

		if labelMatch == nil {
			continue
		}

		label := strings.TrimSpace(labelMatch[1])

		var pctStr string
		if progressMatch != nil {
			pctStr = progressMatch[1]
		} else if valueMatch != nil {
			pctStr = regexp.MustCompile(`[^\d]`).ReplaceAllString(valueMatch[1], "")
		} else {
			continue
		}

		usedPercent, err := strconv.Atoi(pctStr)
		if err != nil {
			usedPercent = 0
		}

		resetText := ""
		if resetMatch != nil {
			resetText = strings.TrimSpace(resetMatch[1])
			resetText = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(resetText, " ")
			resetText = regexp.MustCompile(`\s+`).ReplaceAllString(resetText, " ")
			resetText = strings.TrimSpace(resetText)
			resetText = regexp.MustCompile(`(?i)^.*?Resetuje\s+się\s+za\s*`).ReplaceAllString(resetText, "")
		}

		resetsAt, windowMinutes := parseResetTime(resetText)

		entry := &UsageWindow{
			UsedPercent:   usedPercent,
			ResetsAt:      resetsAt,
			WindowMinutes: windowMinutes,
		}

		labelLower := strings.ToLower(label)
		switch {
		case strings.Contains(labelLower, "kroczące") || strings.Contains(labelLower, "session") || strings.Contains(labelLower, "rolling"):
			entries["rolling"] = entry
		case strings.Contains(labelLower, "tygodniowe") || strings.Contains(labelLower, "weekly"):
			entries["weekly"] = entry
		case strings.Contains(labelLower, "miesięczne") || strings.Contains(labelLower, "monthly"):
			entries["monthly"] = entry
		default:
			entries["rolling"] = entry
		}
	}

	usage := GoUsage{}
	if entries["rolling"] != nil {
		usage.Rolling = entries["rolling"]
	}
	if entries["weekly"] != nil {
		usage.Weekly = entries["weekly"]
	}
	if entries["monthly"] != nil {
		usage.Monthly = entries["monthly"]
	}

	return usage, nil
}

func usageFromJSEmbeddedData(data []*jsWindowData) GoUsage {
	usage := GoUsage{}
	if len(data) > 0 {
		usage.Rolling = usageWindowFromJS(data[0])
	}
	if len(data) > 1 {
		usage.Weekly = usageWindowFromJS(data[1])
	}
	if len(data) > 2 {
		usage.Monthly = usageWindowFromJS(data[2])
	}
	return usage
}

func usageWindowFromJS(data *jsWindowData) *UsageWindow {
	return &UsageWindow{
		UsedPercent:   data.usagePercent,
		WindowMinutes: data.resetInSec / 60,
		ResetsAt:      time.Now().UTC().Add(time.Duration(data.resetInSec) * time.Second).Format(time.RFC3339),
	}
}

// jsWindowData holds exact values extracted from embedded JS.
type jsWindowData struct {
	usagePercent int
	resetInSec   int
}

// extractJSEmbeddedData looks for SolidJS embedded data like:
// $R[30]={status:"ok",resetInSec:13642,usagePercent:14}
// The status field is intentionally not restricted to "ok": a window at
// 100% usage is reported as status:"rate-limited" and must still be parsed.
func extractJSEmbeddedData(html string) []*jsWindowData {
	pattern := regexp.MustCompile(`\$R\[\d+\]=\{[^}]*status:"[^"]*",resetInSec:(\d+),usagePercent:(\d+)[^}]*\}`)
	matches := pattern.FindAllStringSubmatch(html, -1)

	var result []*jsWindowData
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		resetInSec, _ := strconv.Atoi(m[1])
		usagePercent, _ := strconv.Atoi(m[2])
		result = append(result, &jsWindowData{
			usagePercent: usagePercent,
			resetInSec:   resetInSec,
		})
	}
	return result
}

// applyJSEmbeddedData overwrites WindowMinutes and ResetsAt with exact JS values.
// Order: first match = rolling, second = weekly, third = monthly.
func applyJSEmbeddedData(usage GoUsage, data []*jsWindowData) {
	for i, d := range data {
		var entry *UsageWindow
		switch i {
		case 0:
			entry = usage.Rolling
		case 1:
			entry = usage.Weekly
		case 2:
			entry = usage.Monthly
		default:
			continue
		}
		if entry == nil {
			continue
		}
		entry.UsedPercent = d.usagePercent
		entry.WindowMinutes = d.resetInSec / 60
		entry.ResetsAt = time.Now().UTC().Add(time.Duration(d.resetInSec) * time.Second).Format(time.RFC3339)
	}
}

func parseResetTime(text string) (string, int) {
	days := 0
	hours := 0
	minutes := 0

	if m := regexp.MustCompile(`(\d+)\s+(?:dni|dzień)`).FindStringSubmatch(text); m != nil {
		days, _ = strconv.Atoi(m[1])
	}
	if m := regexp.MustCompile(`(\d+)\s+godzin`).FindStringSubmatch(text); m != nil {
		hours, _ = strconv.Atoi(m[1])
	}
	if m := regexp.MustCompile(`(\d+)\s+minut`).FindStringSubmatch(text); m != nil {
		minutes, _ = strconv.Atoi(m[1])
	}

	delta := time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	resetsAt := time.Now().UTC().Add(delta)
	windowMinutes := days*1440 + hours*60 + minutes

	return resetsAt.Format(time.RFC3339), windowMinutes
}
