package opencode

import (
	"strings"
	"testing"
	"time"

	"opentracker/internal/model"
)

const sampleHTML = `<div data-slot="usage"><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie kroczące</span><span data-slot="usage-value"><!--$-->1<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:1%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->4 godzin(y) 19 minut(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie tygodniowe</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->3 dni 23 godzin(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie miesięczne</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->30 dni 21 godzin(y)<!--/--></span></div></div>`

func TestParseHTML_Sample(t *testing.T) {
	usage, err := ParseHTML(sampleHTML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Primary == nil {
		t.Fatal("expected primary entry")
	}
	if usage.Secondary == nil {
		t.Fatal("expected secondary entry")
	}
	if usage.Tertiary == nil {
		t.Fatal("expected tertiary entry")
	}

	// Primary: 1%, window ~259 min (4h19m)
	if usage.Primary.UsedPercent != 1 {
		t.Errorf("primary usedPercent = %d, want 1", usage.Primary.UsedPercent)
	}
	if usage.Primary.WindowMinutes != 259 {
		t.Errorf("primary windowMinutes = %d, want 259", usage.Primary.WindowMinutes)
	}
	if !strings.Contains(usage.Primary.ResetsAt, "T") {
		t.Errorf("primary resetsAt looks invalid: %s", usage.Primary.ResetsAt)
	}

	// Secondary: 0%, window 5700 min (3d23h)
	if usage.Secondary.UsedPercent != 0 {
		t.Errorf("secondary usedPercent = %d, want 0", usage.Secondary.UsedPercent)
	}
	if usage.Secondary.WindowMinutes != 5700 {
		t.Errorf("secondary windowMinutes = %d, want 5700", usage.Secondary.WindowMinutes)
	}

	// Tertiary: 0%, window 44460 min (30d21h)
	if usage.Tertiary.UsedPercent != 0 {
		t.Errorf("tertiary usedPercent = %d, want 0", usage.Tertiary.UsedPercent)
	}
	if usage.Tertiary.WindowMinutes != 44460 {
		t.Errorf("tertiary windowMinutes = %d, want 44460", usage.Tertiary.WindowMinutes)
	}

	// All resetsAt should be in the future
	now := time.Now().UTC()
	for _, entry := range []*model.Entry{usage.Primary, usage.Secondary, usage.Tertiary} {
		ts, err := time.Parse(time.RFC3339, entry.ResetsAt)
		if err != nil {
			t.Errorf("cannot parse resetsAt %q: %v", entry.ResetsAt, err)
			continue
		}
		if !ts.After(now) {
			t.Errorf("resetsAt %q is not in the future (now=%v)", entry.ResetsAt, now)
		}
	}
}
