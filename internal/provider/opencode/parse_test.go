package opencode

import (
	"strings"
	"testing"
	"time"
)

const sampleHTML = `<div data-slot="usage"><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie kroczące</span><span data-slot="usage-value"><!--$-->1<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:1%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->4 godzin(y) 19 minut(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie tygodniowe</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->3 dni 23 godzin(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie miesięczne</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->30 dni 21 godzin(y)<!--/--></span></div></div>`

const sampleHTMLWithJS = `<script>$R[30]={status:"ok",resetInSec:13642,usagePercent:14};$R[31]={status:"ok",resetInSec:273677,usagePercent:29};$R[32]={status:"ok",resetInSec:688810,usagePercent:37}</script>` + sampleHTML

func TestParseHTML_Sample(t *testing.T) {
	usage, err := ParseHTML(sampleHTML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Rolling == nil {
		t.Fatal("expected rolling entry")
	}
	if usage.Weekly == nil {
		t.Fatal("expected weekly entry")
	}
	if usage.Monthly == nil {
		t.Fatal("expected monthly entry")
	}

	// Rolling: 1%, window ~259 min (4h19m)
	if usage.Rolling.UsedPercent != 1 {
		t.Errorf("rolling usedPercent = %d, want 1", usage.Rolling.UsedPercent)
	}
	if usage.Rolling.WindowMinutes != 259 {
		t.Errorf("rolling windowMinutes = %d, want 259", usage.Rolling.WindowMinutes)
	}
	if !strings.Contains(usage.Rolling.ResetsAt, "T") {
		t.Errorf("rolling resetsAt looks invalid: %s", usage.Rolling.ResetsAt)
	}

	// Weekly: 0%, window 5700 min (3d23h)
	if usage.Weekly.UsedPercent != 0 {
		t.Errorf("weekly usedPercent = %d, want 0", usage.Weekly.UsedPercent)
	}
	if usage.Weekly.WindowMinutes != 5700 {
		t.Errorf("weekly windowMinutes = %d, want 5700", usage.Weekly.WindowMinutes)
	}

	// Monthly: 0%, window 44460 min (30d21h)
	if usage.Monthly.UsedPercent != 0 {
		t.Errorf("monthly usedPercent = %d, want 0", usage.Monthly.UsedPercent)
	}
	if usage.Monthly.WindowMinutes != 44460 {
		t.Errorf("monthly windowMinutes = %d, want 44460", usage.Monthly.WindowMinutes)
	}

	// All resetsAt should be in the future
	now := time.Now().UTC()
	for _, entry := range []*UsageWindow{usage.Rolling, usage.Weekly, usage.Monthly} {
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

func TestParseHTML_WithJSEmbeddedData(t *testing.T) {
	usage, err := ParseHTML(sampleHTMLWithJS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Rolling == nil {
		t.Fatal("expected rolling entry")
	}
	if usage.Weekly == nil {
		t.Fatal("expected weekly entry")
	}
	if usage.Monthly == nil {
		t.Fatal("expected monthly entry")
	}

	// JS should override HTML values
	// Rolling: 14%, resetInSec: 13642 -> 227 min
	if usage.Rolling.UsedPercent != 14 {
		t.Errorf("rolling usedPercent = %d, want 14", usage.Rolling.UsedPercent)
	}
	if usage.Rolling.WindowMinutes != 227 {
		t.Errorf("rolling windowMinutes = %d, want 227", usage.Rolling.WindowMinutes)
	}

	// Weekly: 29%, resetInSec: 273677 -> 4561 min
	if usage.Weekly.UsedPercent != 29 {
		t.Errorf("weekly usedPercent = %d, want 29", usage.Weekly.UsedPercent)
	}
	if usage.Weekly.WindowMinutes != 4561 {
		t.Errorf("weekly windowMinutes = %d, want 4561", usage.Weekly.WindowMinutes)
	}

	// Monthly: 37%, resetInSec: 688810 -> 11480 min
	if usage.Monthly.UsedPercent != 37 {
		t.Errorf("monthly usedPercent = %d, want 37", usage.Monthly.UsedPercent)
	}
	if usage.Monthly.WindowMinutes != 11480 {
		t.Errorf("monthly windowMinutes = %d, want 11480", usage.Monthly.WindowMinutes)
	}
}
