package opencode

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const sampleHTML = `<div data-slot="usage"><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie kroczące</span><span data-slot="usage-value"><!--$-->1<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:1%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->4 godzin(y) 19 minut(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie tygodniowe</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->3 dni 23 godzin(y)<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie miesięczne</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->30 dni 21 godzin(y)<!--/--></span></div></div>`

const sampleHTMLWithJS = `<script>$R[30]={status:"ok",resetInSec:13642,usagePercent:14};$R[31]={status:"ok",resetInSec:273677,usagePercent:29};$R[32]={status:"ok",resetInSec:688810,usagePercent:37}</script>` + sampleHTML

const sampleNewHTMLWithJS = `<script>self.$R=self.$R||[];($R[28]=(r,d)=>{r.s(d),r.p.s=1,r.p.v=d})($R[18],$R[33]={mine:!0,useBalance:!1,rollingUsage:$R[34]={status:"ok",resetInSec:18000,usagePercent:0},weeklyUsage:$R[35]={status:"ok",resetInSec:302548,usagePercent:37},monthlyUsage:$R[36]={status:"ok",resetInSec:112881,usagePercent:65}});</script>
<div data-slot="usage">
<div data-hk="0000000100000000000100000500a14004210" data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie kroczące</span><span data-slot="usage-value"><!--$-->0<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:0%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->5 godzin(y) 0 minut(y)<!--/--></span></div>
<div data-hk="0000000100000000000100000500a14004220" data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie tygodniowe</span><span data-slot="usage-value"><!--$-->37<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:37%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->3 dni 12 godzin(y)<!--/--></span></div>
<div data-hk="0000000100000000000100000500a14004230" data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie miesięczne</span><span data-slot="usage-value"><!--$-->65<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:65%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->1 dzień 7 godzin(y)<!--/--></span></div>
</div>`

const sampleNewJSOnly = `<script>rollingUsage:$R[34]={status:"ok",resetInSec:18000,usagePercent:0},weeklyUsage:$R[35]={status:"ok",resetInSec:302548,usagePercent:37},monthlyUsage:$R[36]={status:"ok",resetInSec:112881,usagePercent:65}</script>`

// sampleRateLimitedHTMLWithJS mirrors the real page when the weekly window is
// exhausted: weeklyUsage is reported with status:"rate-limited" instead of
// status:"ok". The parser must still pick it up, otherwise the monthly entry
// shifts into the weekly slot.
const sampleRateLimitedHTMLWithJS = `<script>self.$R=self.$R||[];($R[28]=(r,d)=>{r.s(d),r.p.s=1,r.p.v=d})($R[18],$R[31]={mine:!0,useBalance:!1,allowTraining:!1,region:$R[32]=["us","eu","sg","cn"],rollingUsage:$R[33]={status:"ok",resetInSec:8066,usagePercent:80},weeklyUsage:$R[34]={status:"rate-limited",resetInSec:357468,usagePercent:100},monthlyUsage:$R[35]={status:"ok",resetInSec:2558605,usagePercent:50}});</script>
<div data-slot="usage">
<div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie kroczące</span><span data-slot="usage-value"><!--$-->80<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:80%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->2 godzin(y) 15 minut(y)<!--/--></span></div>
<div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie tygodniowe</span><span data-slot="usage-value"><!--$-->100<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:100%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->4 dni 3 godzin(y)<!--/--></span></div>
<div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">Użycie miesięczne</span><span data-slot="usage-value"><!--$-->50<!--/-->%</span></div><div data-slot="progress"><div data-slot="progress-bar" style="width:50%"></div></div><span data-slot="reset-time"><!--$-->Resetuje się za<!--/--> <!--$-->29 dni 14 godzin(y)<!--/--></span></div>
</div>`

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

func TestParseHTML_NewOpenCodeMarkup(t *testing.T) {
	usage, err := ParseHTML(sampleNewHTMLWithJS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Rolling == nil || usage.Weekly == nil || usage.Monthly == nil {
		t.Fatalf("expected all usage entries, got %#v", usage)
	}
	if usage.Rolling.UsedPercent != 0 || usage.Rolling.WindowMinutes != 300 {
		t.Errorf("rolling = %+v, want 0%% and 300 minutes", usage.Rolling)
	}
	if usage.Weekly.UsedPercent != 37 || usage.Weekly.WindowMinutes != 5042 {
		t.Errorf("weekly = %+v, want 37%% and 5042 minutes", usage.Weekly)
	}
	if usage.Monthly.UsedPercent != 65 || usage.Monthly.WindowMinutes != 1881 {
		t.Errorf("monthly = %+v, want 65%% and 1881 minutes", usage.Monthly)
	}
}

func TestParseHTML_NewOpenCodeJSFallback(t *testing.T) {
	usage, err := ParseHTML(sampleNewJSOnly)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Rolling == nil || usage.Weekly == nil || usage.Monthly == nil {
		t.Fatalf("expected all usage entries, got %#v", usage)
	}
	if usage.Rolling.UsedPercent != 0 || usage.Rolling.WindowMinutes != 300 {
		t.Errorf("rolling = %+v, want 0%% and 300 minutes", usage.Rolling)
	}
	if usage.Weekly.UsedPercent != 37 || usage.Weekly.WindowMinutes != 5042 {
		t.Errorf("weekly = %+v, want 37%% and 5042 minutes", usage.Weekly)
	}
	if usage.Monthly.UsedPercent != 65 || usage.Monthly.WindowMinutes != 1881 {
		t.Errorf("monthly = %+v, want 65%% and 1881 minutes", usage.Monthly)
	}
}

func TestParseHTML_RateLimitedWeekly(t *testing.T) {
	usage, err := ParseHTML(sampleRateLimitedHTMLWithJS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if usage.Rolling == nil || usage.Weekly == nil || usage.Monthly == nil {
		t.Fatalf("expected all usage entries, got %#v", usage)
	}
	// Rolling: 80%, resetInSec 8066 -> 134 min
	if usage.Rolling.UsedPercent != 80 || usage.Rolling.WindowMinutes != 134 {
		t.Errorf("rolling = %+v, want 80%% and 134 minutes", usage.Rolling)
	}
	// Weekly: 100% with status:"rate-limited", resetInSec 357468 -> 5957 min
	if usage.Weekly.UsedPercent != 100 || usage.Weekly.WindowMinutes != 5957 {
		t.Errorf("weekly = %+v, want 100%% and 5957 minutes", usage.Weekly)
	}
	// Monthly: 50%, resetInSec 2558605 -> 42643 min
	if usage.Monthly.UsedPercent != 50 || usage.Monthly.WindowMinutes != 42643 {
		t.Errorf("monthly = %+v, want 50%% and 42643 minutes", usage.Monthly)
	}
}

func TestParseResetTime_PolishSingularDay(t *testing.T) {
	_, minutes := parseResetTime("1 dzień 7 godzin(y)")
	if minutes != 1860 {
		t.Errorf("minutes = %d, want 1860", minutes)
	}
}

// goStatusJSON mirrors a real GET /console/api/go/status response (values from
// a live capture, timestamps parameterized).
const goStatusJSON = `{
  "subscriberUserId": "acc_01TEST",
  "paymentMethodId": "payment_method_01TEST",
  "renewalCurrency": "usd",
  "useBalance": false,
  "cancelAtPeriodEnd": true,
  "renewalPending": false,
  "access": {
    "startsAt": %[1]q,
    "endsAt": %[2]q,
    "cancelAtPeriodEnd": true,
    "meters": {
      "fiveHour": {"startsAt": %[3]q, "resetsAt": %[4]q, "limitMicroCents": "1200000000", "usedMicroCents": "32890547"},
      "week": {"startsAt": %[5]q, "resetsAt": %[6]q, "limitMicroCents": "3000000000", "usedMicroCents": "840107742"},
      "month": {"limitMicroCents": "6000000000", "usedMicroCents": "5627596348"}
    }
  }
}`

func TestParseGoStatusJSON_Meters(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	startsAt := now.Add(-60 * time.Minute)
	fiveHourResets := now.Add(300 * time.Minute)
	endsAt := now.Add(21600 * time.Minute)
	weekStarts := now.Add(-24 * time.Hour)
	weekResets := now.Add(5040 * time.Minute)

	payload := fmt.Sprintf(goStatusJSON,
		startsAt.Format(time.RFC3339), endsAt.Format(time.RFC3339),
		startsAt.Format(time.RFC3339), fiveHourResets.Format(time.RFC3339),
		weekStarts.Format(time.RFC3339), weekResets.Format(time.RFC3339))

	usage, err := ParseGoStatusJSON(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.Rolling == nil || usage.Weekly == nil || usage.Monthly == nil {
		t.Fatalf("expected all usage entries, got %#v", usage)
	}

	// Percents come from used/limit micro-cents (round half up):
	// 32890547/1200000000 -> 3, 840107742/3000000000 -> 28, 5627596348/6000000000 -> 94.
	if usage.Rolling.UsedPercent != 3 {
		t.Errorf("rolling usedPercent = %d, want 3", usage.Rolling.UsedPercent)
	}
	if usage.Weekly.UsedPercent != 28 {
		t.Errorf("weekly usedPercent = %d, want 28", usage.Weekly.UsedPercent)
	}
	if usage.Monthly.UsedPercent != 94 {
		t.Errorf("monthly usedPercent = %d, want 94", usage.Monthly.UsedPercent)
	}

	// Rolling and weekly reset at their own resetsAt; monthly resets at
	// access.endsAt (the paid period end).
	if usage.Rolling.ResetsAt != fiveHourResets.Format(time.RFC3339) {
		t.Errorf("rolling resetsAt = %q, want %q", usage.Rolling.ResetsAt, fiveHourResets.Format(time.RFC3339))
	}
	if usage.Weekly.ResetsAt != weekResets.Format(time.RFC3339) {
		t.Errorf("weekly resetsAt = %q, want %q", usage.Weekly.ResetsAt, weekResets.Format(time.RFC3339))
	}
	if usage.Monthly.ResetsAt != endsAt.Format(time.RFC3339) {
		t.Errorf("monthly resetsAt = %q, want %q", usage.Monthly.ResetsAt, endsAt.Format(time.RFC3339))
	}

	// windowMinutes counts minutes until reset (allow 2 minutes of test skew).
	for _, tc := range []struct {
		name    string
		got     int
		wantMin int
	}{
		{"rolling", usage.Rolling.WindowMinutes, 300},
		{"weekly", usage.Weekly.WindowMinutes, 5040},
		{"monthly", usage.Monthly.WindowMinutes, 21600},
	} {
		if tc.got < tc.wantMin-2 || tc.got > tc.wantMin {
			t.Errorf("%s windowMinutes = %d, want ~%d", tc.name, tc.got, tc.wantMin)
		}
	}
}

func TestParseGoStatusJSON_MissingAccess(t *testing.T) {
	if _, err := ParseGoStatusJSON(`{"subscriberUserId":"acc_01TEST","renewalCurrency":"usd"}`); err == nil {
		t.Fatal("expected error for response without access meters")
	}
	if _, err := ParseGoStatusJSON(`{not json`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseGoStatusJSON_IdleRolling(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endsAt := now.Add(21600 * time.Minute)
	weekResets := now.Add(5040 * time.Minute)
	payload := fmt.Sprintf(`{
	  "access": {
	    "endsAt": %[1]q,
	    "meters": {
	      "fiveHour": {"limitMicroCents": "0", "usedMicroCents": "0"},
	      "week": {"startsAt": %[2]q, "resetsAt": %[3]q, "limitMicroCents": "3000000000", "usedMicroCents": "0"},
	      "month": {"limitMicroCents": "6000000000", "usedMicroCents": "0"}
	    }
	  }
	}`, endsAt.Format(time.RFC3339), now.Format(time.RFC3339), weekResets.Format(time.RFC3339))

	usage, err := ParseGoStatusJSON(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.Rolling == nil {
		t.Fatal("expected rolling entry")
	}
	if usage.Rolling.UsedPercent != 0 {
		t.Errorf("rolling usedPercent = %d, want 0 (zero limit)", usage.Rolling.UsedPercent)
	}
	if usage.Rolling.ResetsAt != "" || usage.Rolling.WindowMinutes != 0 {
		t.Errorf("idle rolling = %+v, want empty resetsAt and 0 minutes", usage.Rolling)
	}
}

func TestParseGoStatusJSON_NumericMicroCents(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	resetsAt := now.Add(300 * time.Minute)
	payload := fmt.Sprintf(`{"access":{"endsAt":%[1]q,"meters":{"fiveHour":{"resetsAt":%[2]q,"limitMicroCents":1200000000,"usedMicroCents":32890547},"week":{"startsAt":%[1]q,"resetsAt":%[1]q,"limitMicroCents":3000000000,"usedMicroCents":0},"month":{"limitMicroCents":6000000000,"usedMicroCents":0}}}}`,
		resetsAt.Format(time.RFC3339), resetsAt.Format(time.RFC3339))

	usage, err := ParseGoStatusJSON(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage.Rolling.UsedPercent != 3 {
		t.Errorf("rolling usedPercent = %d, want 3", usage.Rolling.UsedPercent)
	}
}

func TestUsedPercentOf_RoundHalfUp(t *testing.T) {
	for _, tc := range []struct {
		used, limit string
		want        int
	}{
		{"505", "1000", 51},
		{"504", "1000", 50},
		{"0", "0", 0},
		{"100", "0", 0},
		{"1000", "1000", 100},
	} {
		got := usedPercentOf(microCents{raw: tc.used}, microCents{raw: tc.limit})
		if got != tc.want {
			t.Errorf("usedPercentOf(%s, %s) = %d, want %d", tc.used, tc.limit, got, tc.want)
		}
	}
}

func TestParseZenBillingJSON(t *testing.T) {
	payload := `{
	  "billingMode": "prepaid",
	  "mode": "pay-as-you-go",
	  "balanceMicroCents": "250000000",
	  "creditLimitMicroCents": null,
	  "availableMicroCents": "250000000",
	  "canPurchaseCredits": true,
	  "canEnableAutoRecharge": true,
	  "canEnrollInPrepaid": false
	}`
	billing, err := ParseZenBillingJSON(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if billing.BillingMode != "prepaid" || billing.Mode != "pay-as-you-go" {
		t.Errorf("billing = %q/%q, want prepaid/pay-as-you-go", billing.BillingMode, billing.Mode)
	}
	if billing.BalanceMicroCents != "250000000" || billing.BalanceDollars != 2.5 {
		t.Errorf("balance = %q/%v, want 250000000 / 2.5", billing.BalanceMicroCents, billing.BalanceDollars)
	}
	if billing.AvailableDollars != 2.5 {
		t.Errorf("availableDollars = %v, want 2.5", billing.AvailableDollars)
	}
	if billing.CreditLimitMicroCents != nil || billing.CreditLimitDollars != nil {
		t.Errorf("creditLimit = %v/%v, want nil/nil", billing.CreditLimitMicroCents, billing.CreditLimitDollars)
	}
	if !billing.CanPurchaseCredits || !billing.CanEnableAutoRecharge || billing.CanEnrollInPrepaid {
		t.Errorf("unexpected capability flags: %+v", billing)
	}

	withLimit := `{"billingMode":"credit","mode":"subscription","balanceMicroCents":"0","creditLimitMicroCents":"50000000000","availableMicroCents":"49000000000"}`
	billing, err = ParseZenBillingJSON(withLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if billing.CreditLimitMicroCents == nil || *billing.CreditLimitMicroCents != "50000000000" {
		t.Errorf("creditLimitMicroCents = %v, want 50000000000", billing.CreditLimitMicroCents)
	}
	if billing.CreditLimitDollars == nil || *billing.CreditLimitDollars != 500 {
		t.Errorf("creditLimitDollars = %v, want 500", billing.CreditLimitDollars)
	}

	if _, err := ParseZenBillingJSON(`{}`); err == nil {
		t.Fatal("expected error for empty billing payload")
	}
	if _, err := ParseZenBillingJSON(`{oops`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
