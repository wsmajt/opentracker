package opencode

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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
