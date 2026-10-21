package localstats

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opentracker/internal/cache"
)

const validStats = `{"range":{"from":100,"to":200},"sessions":3,"tokens":{"input":10,"output":20,"reasoning":30,"cache":{"read":40,"write":50}},"cost":99,"models":[]}`

func TestParseTokenSumAndOlderCounters(t *testing.T) {
	t.Run("all counters", func(t *testing.T) {
		got, err := parse([]byte(validStats))
		if err != nil {
			t.Fatal(err)
		}
		if got.total != 150 || got.sessions != 3 || got.rangeValue != (Range{From: 100, To: 200}) {
			t.Fatalf("parse() = %+v", got)
		}
	})
	t.Run("older response omits optional counters", func(t *testing.T) {
		got, err := parse([]byte(`{"range":{"from":100,"to":200},"sessions":1,"tokens":{"input":8,"output":2}}`))
		if err != nil || got.total != 10 {
			t.Fatalf("parse() = %+v, %v", got, err)
		}
	})
}

func TestParseValidZeroAndRejectsInvalidInput(t *testing.T) {
	zero := `{"range":{"from":0,"to":0},"sessions":0,"tokens":{"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}}}`
	got, err := parse([]byte(zero))
	if err != nil || got.total != 0 || got.sessions != 0 {
		t.Fatalf("parse zero = %+v, %v", got, err)
	}
	invalid := []string{
		`null`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":null}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":null,"output":2}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":1,"output":2,"reasoning":null}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":1,"output":2,"reasoning":"4"}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":1,"output":2,"cache":null}}`,
		`{"range":{"from":1,"to":2},"tokens":{"input":1,"output":2}}`,
		`{"range":{"from":1,"to":2},"sessions":-1,"tokens":{"input":1,"output":2}}`,
		`{"range":{"from":2,"to":1},"sessions":1,"tokens":{"input":1,"output":2}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":9223372036854775807,"output":1}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":1.5,"output":2}}`,
		`{"range":{"from":1,"to":2},"sessions":1,"tokens":{"input":1,"output":2}} trailing`,
	}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			if _, err := parse([]byte(input)); err == nil {
				t.Fatal("parse() accepted invalid input")
			}
		})
	}
}

func TestTodayCacheForceAndLocalMidnight(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	now := time.Date(2026, 10, 21, 23, 59, 0, 0, loc)
	calls := 0
	runner := func(_ context.Context, binary string, args ...string) ([]byte, error) {
		calls++
		if binary != "opencode" || strings.Join(args, "|") != "stats|--json|--standalone|--days|0" {
			t.Fatalf("runner args = %q %q", binary, args)
		}
		return validStatsFor(now), nil
	}
	client := Client{Cache: cache.New(t.TempDir()), Run: runner, Now: func() time.Time { return now }, Identity: "profile"}
	first, err := client.Today(context.Background(), false)
	if err != nil || first.TotalTokens != 150 || first.Source != "opencode-local" || first.Period != "today" {
		t.Fatalf("Today() = %+v, %v", first, err)
	}
	if _, err := client.Today(context.Background(), false); err != nil || calls != 1 {
		t.Fatalf("cached Today() calls=%d, err=%v", calls, err)
	}
	if _, err := client.Today(context.Background(), true); err != nil || calls != 2 {
		t.Fatalf("forced Today() calls=%d, err=%v", calls, err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := client.Today(context.Background(), false); err != nil || calls != 3 {
		t.Fatalf("midnight Today() calls=%d, err=%v", calls, err)
	}
}

func TestTodayRejectsYesterdayRangeAndMidnightCrossing(t *testing.T) {
	loc := time.FixedZone("test", -7*60*60)
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, loc)
	yesterday := start.AddDate(0, 0, -1)
	now := start.Add(23*time.Hour + 59*time.Minute)
	client := Client{
		Cache: cache.New(t.TempDir()),
		Now:   func() time.Time { return now },
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return statsForRange(yesterday.UnixMilli(), start.UnixMilli()), nil
		},
	}
	if _, err := client.Today(context.Background(), false); !errors.Is(err, ErrRangeNotToday) {
		t.Fatalf("yesterday range error = %v", err)
	}

	beforeMidnight := now
	client.Now = func() time.Time {
		if now.Equal(beforeMidnight) {
			now = beforeMidnight.Add(2 * time.Minute)
		}
		return now
	}
	client.Run = func(context.Context, string, ...string) ([]byte, error) {
		return validStatsFor(beforeMidnight), nil
	}
	if _, err := client.Today(context.Background(), true); !errors.Is(err, ErrRangeNotToday) {
		t.Fatalf("midnight-crossing range error = %v", err)
	}
}

func TestTodayRejectsInvalidCachedResults(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	valid := Result{
		Source: "opencode-local", Period: "today", TotalTokens: 12, Sessions: 2,
		UpdatedAt: now.UTC().Format(time.RFC3339), Range: Range{From: start.UnixMilli(), To: now.UnixMilli()},
	}
	invalid := []struct {
		name   string
		mutate func(*Result)
	}{
		{"source", func(result *Result) { result.Source = "other" }},
		{"period", func(result *Result) { result.Period = "week" }},
		{"yesterday range", func(result *Result) { result.Range.From -= int64(24 * time.Hour / time.Millisecond) }},
		{"negative token total", func(result *Result) { result.TotalTokens = -1 }},
		{"negative sessions", func(result *Result) { result.Sessions = -1 }},
		{"invalid timestamp", func(result *Result) { result.UpdatedAt = "yesterday" }},
		{"yesterday timestamp", func(result *Result) { result.UpdatedAt = now.AddDate(0, 0, -1).UTC().Format(time.RFC3339) }},
		{"stale timestamp", func(result *Result) { result.UpdatedAt = now.Add(-10 * time.Minute).UTC().Format(time.RFC3339) }},
		{"future timestamp", func(result *Result) { result.UpdatedAt = now.Add(time.Minute).UTC().Format(time.RFC3339) }},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			storage := cache.New(t.TempDir())
			bad := valid
			test.mutate(&bad)
			key := cacheKey("profile", now)
			if err := storage.Set(key, bad, cacheTTL); err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := Client{
				Cache: storage, Identity: "profile", Now: func() time.Time { return now },
				Run: func(context.Context, string, ...string) ([]byte, error) {
					calls++
					return validStatsFor(now), nil
				},
			}
			if _, err := client.Today(context.Background(), false); err != nil || calls != 1 {
				t.Fatalf("Today() calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestRangeTodayUsesInclusiveDayBoundaries(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	if !rangeIsToday(Range{From: start.UnixMilli(), To: end.UnixMilli() - 1}, now) {
		t.Fatal("today range with inclusive day boundaries was rejected")
	}
	if rangeIsToday(Range{From: start.UnixMilli(), To: end.UnixMilli()}, now) {
		t.Fatal("next midnight was accepted as today's inclusive endpoint")
	}
}

func TestTodaySurfacesRunnerError(t *testing.T) {
	client := Client{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, ErrMissingBinary }}
	_, err := client.Today(context.Background(), false)
	if !errors.Is(err, ErrMissingBinary) {
		t.Fatalf("Today() error = %v", err)
	}
}

func TestRunCommandUsesLiteralArgsAndSafeErrors(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	binary := filepath.Join(dir, "opencode")
	writeExecutable(t, binary, "#!/bin/sh\nprintf '%s\\n' \"$@\" > "+argsPath+"\nprintf '%s' '"+validStats+"'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, err := runCommand(context.Background(), "opencode", "stats", "--json", "--standalone", "--days", "0")
	if err != nil || string(stdout) != validStats {
		t.Fatalf("runCommand() = %q, %v", stdout, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil || string(args) != "stats\n--json\n--standalone\n--days\n0\n" {
		t.Fatalf("received args = %q, %v", args, err)
	}

	writeExecutable(t, binary, "#!/bin/sh\nprintf '%s' '/private/path: failed' >&2\nexit 1\n")
	_, err = runCommand(context.Background(), "opencode", "stats")
	if !errors.Is(err, ErrCommandFailed) || strings.Contains(err.Error(), "/private/path") {
		t.Fatalf("nonzero error = %v", err)
	}

	writeExecutable(t, binary, "#!/bin/sh\nexec sleep 1\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = runCommand(ctx, "opencode", "stats")
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("timeout error = %v", err)
	}

	if _, err := runCommand(context.Background(), filepath.Join(dir, "missing")); !errors.Is(err, ErrMissingBinary) {
		t.Fatalf("missing binary error = %v", err)
	}

	writeExecutable(t, binary, "#!/bin/sh\nprintf '%s' '"+validStats+"'\nprintf '%1100000s' ''\n")
	if _, err := runCommand(context.Background(), "opencode", "stats"); !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("oversized stdout error = %v", err)
	}
}

func validStatsFor(now time.Time) []byte {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return statsForRange(start.UnixMilli(), now.UnixMilli())
}

func statsForRange(from, to int64) []byte {
	return []byte(fmt.Sprintf(`{"range":{"from":%d,"to":%d},"sessions":3,"tokens":{"input":10,"output":20,"reasoning":30,"cache":{"read":40,"write":50}},"cost":99,"models":[]}`, from, to))
}

func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
		t.Fatal(err)
	}
}
