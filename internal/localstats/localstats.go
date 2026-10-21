package localstats

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	"opentracker/internal/cache"
)

const cacheTTL = 5 * time.Minute

var (
	ErrMissingBinary  = errors.New("OpenCode CLI was not found")
	ErrTimedOut       = errors.New("OpenCode stats command timed out")
	ErrCommandFailed  = errors.New("OpenCode stats command failed")
	ErrOutputTooLarge = errors.New("OpenCode stats output exceeded the size limit")
	ErrRangeNotToday  = errors.New("OpenCode stats range does not match today")
)

// Result contains today's aggregate OpenCode usage.
type Result struct {
	Source      string `json:"source"`
	Period      string `json:"period"`
	TotalTokens int64  `json:"totalTokens"`
	Sessions    int64  `json:"sessions"`
	UpdatedAt   string `json:"updatedAt"`
	Range       Range  `json:"range"`
}

// Range is the epoch-millisecond interval reported by OpenCode.
type Range struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Runner executes the OpenCode stats command and returns its stdout.
type Runner func(context.Context, string, ...string) ([]byte, error)

// Client retrieves stats from the local OpenCode CLI and caches the result.
type Client struct {
	Cache    *cache.Cache
	Run      Runner
	Now      func() time.Time
	Identity string
}

// Today returns cached or fresh local stats. Identity should include the
// user's OpenCode data location so separate local profiles do not share data.
func (c *Client) Today(ctx context.Context, force bool) (Result, error) {
	now := c.currentTime()
	key := cacheKey(c.Identity, now)
	if !force && c.Cache != nil {
		var cached Result
		if c.Cache.Get(key, &cached) && validCachedResult(cached, c.currentTime()) {
			return cached, nil
		}
	}
	runner := c.Run
	if runner == nil {
		runner = runCommand
	}
	raw, err := runner(ctx, "opencode", "stats", "--json", "--standalone", "--days", "0")
	if err != nil {
		return Result{}, err
	}
	parsed, err := parse(raw)
	if err != nil {
		return Result{}, err
	}
	now = c.currentTime()
	if !rangeIsToday(parsed.rangeValue, now) {
		return Result{}, ErrRangeNotToday
	}
	result := Result{
		Source:      "opencode-local",
		Period:      "today",
		TotalTokens: parsed.total,
		Sessions:    parsed.sessions,
		UpdatedAt:   now.UTC().Format(time.RFC3339),
		Range:       parsed.rangeValue,
	}
	if c.Cache != nil {
		_ = c.Cache.Set(cacheKey(c.Identity, now), result, cacheTTL)
	}
	return result, nil
}

func (c *Client) currentTime() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func validCachedResult(result Result, now time.Time) bool {
	if result.Source != "opencode-local" || result.Period != "today" || result.TotalTokens < 0 || result.Sessions < 0 || !rangeIsToday(result.Range, now) {
		return false
	}
	updated, err := time.Parse(time.RFC3339, result.UpdatedAt)
	return err == nil && !updated.After(now) && now.Sub(updated) <= cacheTTL && sameLocalDate(updated, now)
}

func rangeIsToday(value Range, now time.Time) bool {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	end := start.AddDate(0, 0, 1)
	from := time.UnixMilli(value.From)
	to := time.UnixMilli(value.To)
	// Both reported endpoints are inclusive timestamps and must fall on the
	// current local date. The next midnight belongs to the following day.
	return value.From >= 0 && value.To >= value.From && !from.Before(start) && from.Before(end) && !to.Before(start) && to.Before(end)
}

func sameLocalDate(left, right time.Time) bool {
	left = left.In(right.Location())
	return left.Year() == right.Year() && left.Month() == right.Month() && left.Day() == right.Day()
}

func cacheKey(identity string, now time.Time) string {
	zone, offset := now.Zone()
	identity += "\x00" + now.Format("2006-01-02") + "\x00" + zone + fmt.Sprintf("\x00%d", offset)
	sum := sha256.Sum256([]byte(identity))
	return "opencode-local-" + hex.EncodeToString(sum[:])
}

type rawStats struct {
	Range    *rawRange    `json:"range"`
	Sessions *json.Number `json:"sessions"`
	Tokens   *rawTokens   `json:"tokens"`
}

type rawRange struct {
	From *json.Number `json:"from"`
	To   *json.Number `json:"to"`
}

type rawTokens struct {
	Input     *json.Number    `json:"input"`
	Output    *json.Number    `json:"output"`
	Reasoning json.RawMessage `json:"reasoning"`
	Cache     json.RawMessage `json:"cache"`
}

type rawCache struct {
	Read  json.RawMessage `json:"read"`
	Write json.RawMessage `json:"write"`
}

type parsedStats struct {
	total      int64
	sessions   int64
	rangeValue Range
}

func parse(data []byte) (parsedStats, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var input rawStats
	if err := decoder.Decode(&input); err != nil {
		return parsedStats{}, errors.New("OpenCode returned invalid stats JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return parsedStats{}, errors.New("OpenCode returned invalid stats JSON")
	}
	if input.Range == nil || input.Range.From == nil || input.Range.To == nil || input.Sessions == nil || input.Tokens == nil || input.Tokens.Input == nil || input.Tokens.Output == nil {
		return parsedStats{}, errors.New("OpenCode stats JSON is missing required fields")
	}
	from, err := number(input.Range.From)
	if err != nil {
		return parsedStats{}, errors.New("OpenCode stats JSON has an invalid range")
	}
	to, err := number(input.Range.To)
	if err != nil || to < from {
		return parsedStats{}, errors.New("OpenCode stats JSON has an invalid range")
	}
	sessions, err := number(input.Sessions)
	if err != nil {
		return parsedStats{}, errors.New("OpenCode stats JSON has invalid sessions")
	}
	values := []*json.Number{input.Tokens.Input, input.Tokens.Output}
	reasoning, err := optionalNumber(input.Tokens.Reasoning)
	if err != nil {
		return parsedStats{}, errors.New("OpenCode stats JSON has invalid token counts")
	}
	if reasoning != nil {
		values = append(values, reasoning)
	}
	if len(input.Tokens.Cache) != 0 {
		if bytes.Equal(bytes.TrimSpace(input.Tokens.Cache), []byte("null")) {
			return parsedStats{}, errors.New("OpenCode stats JSON has invalid token counts")
		}
		var cacheCounts rawCache
		if err := json.Unmarshal(input.Tokens.Cache, &cacheCounts); err != nil {
			return parsedStats{}, errors.New("OpenCode stats JSON has invalid token counts")
		}
		for _, raw := range []json.RawMessage{cacheCounts.Read, cacheCounts.Write} {
			count, err := optionalNumber(raw)
			if err != nil {
				return parsedStats{}, errors.New("OpenCode stats JSON has invalid token counts")
			}
			if count != nil {
				values = append(values, count)
			}
		}
	}
	var total int64
	for _, value := range values {
		count, err := number(value)
		if err != nil || count > math.MaxInt64-total {
			return parsedStats{}, errors.New("OpenCode stats JSON has invalid token counts")
		}
		total += count
	}
	return parsedStats{total: total, sessions: sessions, rangeValue: Range{From: from, To: to}}, nil
}

func optionalNumber(raw json.RawMessage) (*json.Number, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, errors.New("invalid number")
	}
	value := json.Number(string(bytes.TrimSpace(raw)))
	if _, err := value.Int64(); err != nil {
		return nil, err
	}
	return &value, nil
}

func number(value *json.Number) (int64, error) {
	if value == nil {
		return 0, errors.New("missing number")
	}
	n, err := value.Int64()
	if err != nil || n < 0 {
		return 0, errors.New("invalid number")
	}
	return n, nil
}

type cappedBuffer struct {
	data     []byte
	max      int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.max - len(b.data)
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		b.data = append(b.data, p[:remaining]...)
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func runCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr cappedBuffer
	stdout.max = 1 << 20
	stderr.max = 64 << 10
	cmd.WaitDelay = time.Second
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimedOut
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return nil, ErrMissingBinary
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrMissingBinary
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrMissingBinary
		}
		return nil, ErrCommandFailed
	}
	if stdout.overflow {
		return nil, ErrOutputTooLarge
	}
	return stdout.data, nil
}
