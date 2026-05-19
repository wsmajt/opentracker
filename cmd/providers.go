package cmd

import (
	"fmt"
	"sort"
	"strings"

	"opentracker/internal/provider"
)

// SetupProviderHelp updates command help text with the current list of providers.
// Call this after all provider packages have been imported.
func SetupProviderHelp() {
	names := provider.List()
	sort.Strings(names)

	loginCmd.Long = buildLoginLong(names)
	fetchCmd.Long = buildFetchLong(names)
}

func printProviderList(action string) {
	names := provider.List()
	if len(names) == 0 {
		fmt.Println("No providers registered.")
		return
	}
	sort.Strings(names)

	if action == "login" {
		names = groupOpencode(names)
	}

	fmt.Println("Available providers:")
	for _, name := range names {
		if action == "login" {
			fmt.Printf("  %-12s %s\n", name, providerLoginDescription(name))
		} else {
			fmt.Printf("  %s\n", name)
		}
	}
	if action == "fetch" {
		fmt.Println()
		fmt.Println("Use 'all' to fetch from every configured provider.")
	}
	fmt.Println()
	fmt.Printf("Usage: opentracker %s <provider>\n", action)
}

func buildLoginLong(names []string) string {
	names = groupOpencode(names)
	var b strings.Builder
	b.WriteString("Open the login page or run the login flow for a provider.\n\n")
	b.WriteString("Available providers:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %-12s %s\n", name, providerLoginDescription(name))
	}
	return b.String()
}

func buildFetchLong(names []string) string {
	var b strings.Builder
	b.WriteString("Fetch usage data from a provider.\n\n")
	b.WriteString("Available providers:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  %s\n", name)
	}
	b.WriteString("\nUse 'all' to fetch from every configured provider.\n")
	return b.String()
}

func groupOpencode(names []string) []string {
	var result []string
	hasOpencode := false
	for _, name := range names {
		if strings.HasPrefix(name, "opencode-") {
			hasOpencode = true
		} else {
			result = append(result, name)
		}
	}
	if hasOpencode {
		result = append(result, "opencode")
	}
	sort.Strings(result)
	return result
}

func providerLoginDescription(name string) string {
	switch name {
	case "codex":
		return "Run 'codex login'"
	case "opencode":
		return "Open opencode.ai and import browser cookies"
	default:
		return ""
	}
}
