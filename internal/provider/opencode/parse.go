package opencode

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"opentracker/internal/model"
)

// ParseHTML extracts usage data from OpenCode HTML.
func ParseHTML(html string) (model.Usage, error) {
	// Remove HTML comments
	html = regexp.MustCompile(`<!--\$?|--/?>`).ReplaceAllString(html, "")

	// Split by usage-item blocks
	parts := regexp.MustCompile(`<div\s+data-slot="usage-item"[^>]*>`).Split(html, -1)
	if len(parts) < 2 {
		return model.Usage{}, fmt.Errorf("no usage items found")
	}

	var entries = make(map[string]*model.Entry)

	for i, part := range parts[1:] {
		// Find the end of this block (next usage-item or end)
		endIdx := strings.Index(part, `<div data-slot="usage-item"`)
		if endIdx != -1 {
			part = part[:endIdx]
		}

		labelMatch := regexp.MustCompile(`<span\s+data-slot="usage-label"[^>]*>(.*?)</span>`).FindStringSubmatch(part)
		progressMatch := regexp.MustCompile(`<div\s+data-slot="progress-bar"[^>]*style="width:\s*(\d+)%?"[^>]*>`).FindStringSubmatch(part)
		valueMatch := regexp.MustCompile(`<span\s+data-slot="usage-value"[^>]*>(.*?)</span>`).FindStringSubmatch(part)
		resetMatch := regexp.MustCompile(`<span\s+data-slot="reset-time"[^>]*>(.*?)</span>`).FindStringSubmatch(part)

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
			resetText = regexp.MustCompile(`(?i)^.*?Resetuje\s+się\s+za\s*`).ReplaceAllString(resetText, "")
		}

		resetsAt, windowMinutes := parseResetTime(resetText)

		entry := &model.Entry{
			UsedPercent:   usedPercent,
			ResetsAt:      resetsAt,
			WindowMinutes: windowMinutes,
		}

		labelLower := strings.ToLower(label)
		if strings.Contains(labelLower, "kroczące") || strings.Contains(labelLower, "session") || strings.Contains(labelLower, "rolling") {
			entries["primary"] = entry
		} else if strings.Contains(labelLower, "tygodniowe") || strings.Contains(labelLower, "weekly") {
			entries["secondary"] = entry
		} else if strings.Contains(labelLower, "miesięczne") || strings.Contains(labelLower, "monthly") {
			entries["tertiary"] = entry
		} else {
			entries["primary"] = entry
		}

		_ = i // silence unused variable warning if any
	}

	usage := model.Usage{}
	if entries["primary"] != nil {
		usage.Primary = entries["primary"]
	}
	if entries["secondary"] != nil {
		usage.Secondary = entries["secondary"]
	}
	if entries["tertiary"] != nil {
		usage.Tertiary = entries["tertiary"]
	}

	return usage, nil
}

func parseResetTime(text string) (string, int) {
	days := 0
	hours := 0
	minutes := 0

	if m := regexp.MustCompile(`(\d+)\s+dni`).FindStringSubmatch(text); m != nil {
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
