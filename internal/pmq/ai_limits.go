package pmq

import (
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

// AIPrice is an operator-supplied estimate. It is never treated as billing data.
type AIPrice struct {
	InputUSDPerMillion  float64 `json:"input_usd_per_million"`
	OutputUSDPerMillion float64 `json:"output_usd_per_million"`
	Provenance          string  `json:"provenance"`
}

type AILimits struct {
	GlobalRPM           int
	PerAppRPM           int
	GlobalConcurrency   int
	PerAppConcurrency   int
	MaxRequestBytes     int64
	MaxResponseBytes    int64
	MaxOutputTokens     int
	DefaultOutputTokens int
	Timeout             time.Duration
	GlobalBudgetUSD     float64
	PerAppBudgetUSD     float64
	Pricing             map[string]AIPrice
}

func defaultAILimits() AILimits {
	return AILimits{
		GlobalRPM: 120, PerAppRPM: 30,
		GlobalConcurrency: 16, PerAppConcurrency: 4,
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 2 << 20,
		MaxOutputTokens: 32768, DefaultOutputTokens: 1024,
		Timeout: 45 * time.Second,
	}
}

func normalizeAILimits(l AILimits) AILimits {
	d := defaultAILimits()
	if l.GlobalRPM <= 0 {
		l.GlobalRPM = d.GlobalRPM
	}
	if l.PerAppRPM <= 0 {
		l.PerAppRPM = d.PerAppRPM
	}
	if l.GlobalConcurrency <= 0 {
		l.GlobalConcurrency = d.GlobalConcurrency
	}
	if l.PerAppConcurrency <= 0 {
		l.PerAppConcurrency = d.PerAppConcurrency
	}
	if l.MaxRequestBytes <= 0 {
		l.MaxRequestBytes = d.MaxRequestBytes
	}
	if l.MaxResponseBytes <= 0 {
		l.MaxResponseBytes = d.MaxResponseBytes
	}
	if l.MaxOutputTokens <= 0 {
		l.MaxOutputTokens = d.MaxOutputTokens
	}
	if l.DefaultOutputTokens <= 0 {
		l.DefaultOutputTokens = d.DefaultOutputTokens
		if l.DefaultOutputTokens > l.MaxOutputTokens {
			l.DefaultOutputTokens = l.MaxOutputTokens
		}
	}
	if l.Timeout <= 0 {
		l.Timeout = d.Timeout
	}
	return l
}

func validateAILimits(l AILimits, models map[string]ModelBinding) error {
	if l.GlobalConcurrency > 1024 || l.PerAppConcurrency > 1024 || l.GlobalRPM > 1_000_000 || l.PerAppRPM > 1_000_000 {
		return errors.New("AI rate or concurrency limits exceed supported maximums")
	}
	if l.MaxRequestBytes > 8<<20 || l.MaxResponseBytes > 16<<20 {
		return errors.New("AI request and response size limits may not exceed 8 MiB and 16 MiB")
	}
	if l.MaxOutputTokens > 32768 || l.DefaultOutputTokens > l.MaxOutputTokens {
		return errors.New("AI default/max output token limits are inconsistent or exceed 32768")
	}
	if l.Timeout > 10*time.Minute {
		return errors.New("AI timeout may not exceed 10 minutes")
	}
	if l.GlobalBudgetUSD < 0 || l.PerAppBudgetUSD < 0 || math.IsNaN(l.GlobalBudgetUSD) || math.IsNaN(l.PerAppBudgetUSD) || math.IsInf(l.GlobalBudgetUSD, 0) || math.IsInf(l.PerAppBudgetUSD, 0) {
		return errors.New("AI budget limits must be finite non-negative amounts")
	}
	if l.GlobalBudgetUSD > 0 || l.PerAppBudgetUSD > 0 {
		for alias := range models {
			if _, ok := l.Pricing[alias]; !ok {
				return fmt.Errorf("AI budget requires pricing for model alias %q", alias)
			}
		}
	}
	for alias, price := range l.Pricing {
		if _, ok := models[alias]; !ok {
			return fmt.Errorf("AI pricing references unknown model alias %q", alias)
		}
		if price.InputUSDPerMillion < 0 || price.OutputUSDPerMillion < 0 || price.InputUSDPerMillion > 1e9 || price.OutputUSDPerMillion > 1e9 || math.IsNaN(price.InputUSDPerMillion) || math.IsNaN(price.OutputUSDPerMillion) || math.IsInf(price.InputUSDPerMillion, 0) || math.IsInf(price.OutputUSDPerMillion, 0) {
			return fmt.Errorf("AI pricing for alias %q must be finite and non-negative", alias)
		}
		if strings.TrimSpace(price.Provenance) == "" || len(price.Provenance) > 256 || strings.ContainsAny(price.Provenance, "\r\n") {
			return fmt.Errorf("AI pricing for alias %q requires safe provenance text (1 to 256 characters)", alias)
		}
	}
	return nil
}

type aiAdmission struct {
	mu             sync.Mutex
	limits         AILimits
	now            func() time.Time
	minute         int64
	day            string
	globalRequests int
	appRequests    map[string]int
	globalActive   int
	appActive      map[string]int
	globalSpend    float64
	appSpend       map[string]float64
}

type aiReservation struct {
	app            string
	alias          string
	day            string
	cost           float64
	priced         bool
	inputEstimate  int
	outputEstimate int
}

func newAIAdmission(l AILimits) *aiAdmission {
	return &aiAdmission{limits: l, now: time.Now, appRequests: make(map[string]int), appActive: make(map[string]int), appSpend: make(map[string]float64)}
}

func estimateInputTokens(req ChatRequest) int {
	// One token per input byte plus message overhead is deliberately conservative.
	total := 0
	for _, message := range req.Messages {
		total += len(message.Content) + 4
	}
	return total
}

func estimateAICost(price AIPrice, inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)*price.InputUSDPerMillion + float64(outputTokens)*price.OutputUSDPerMillion) / 1_000_000
}

func retryUntil(next, now time.Time) int {
	seconds := int(next.Sub(now).Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (a *aiAdmission) reset(now time.Time) {
	minute := now.Unix() / 60
	if a.minute != minute {
		a.minute, a.globalRequests, a.appRequests = minute, 0, make(map[string]int)
	}
	day := now.UTC().Format("2006-01-02")
	if a.day != day {
		a.day, a.globalSpend, a.appSpend = day, 0, make(map[string]float64)
	}
}

func (a *aiAdmission) admit(app, alias string, req ChatRequest) (*aiReservation, string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.reset(now)
	if a.globalRequests >= a.limits.GlobalRPM || a.appRequests[app] >= a.limits.PerAppRPM {
		return nil, "rate_limit", retryUntil(time.Unix((now.Unix()/60+1)*60, 0), now)
	}
	a.globalRequests++
	a.appRequests[app]++
	if a.globalActive >= a.limits.GlobalConcurrency || a.appActive[app] >= a.limits.PerAppConcurrency {
		return nil, "concurrency_limit", 1
	}
	output := a.limits.DefaultOutputTokens
	if req.MaxTokens != nil {
		output = *req.MaxTokens
	}
	input := estimateInputTokens(req)
	price, priced := a.limits.Pricing[alias]
	cost := 0.0
	if priced {
		cost = estimateAICost(price, input, output)
	}
	if a.limits.GlobalBudgetUSD > 0 && a.globalSpend+cost > a.limits.GlobalBudgetUSD || a.limits.PerAppBudgetUSD > 0 && a.appSpend[app]+cost > a.limits.PerAppBudgetUSD {
		midnight := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return nil, "budget_limit", retryUntil(midnight, now)
	}
	a.globalActive++
	a.appActive[app]++
	a.globalSpend += cost
	a.appSpend[app] += cost
	return &aiReservation{app: app, alias: alias, day: a.day, cost: cost, priced: priced, inputEstimate: input, outputEstimate: output}, "", 0
}

func (a *aiAdmission) finish(r *aiReservation, usage *ChatUsage) (float64, string) {
	if r == nil {
		return 0, "unavailable"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.globalActive > 0 {
		a.globalActive--
	}
	if a.appActive[r.app] > 1 {
		a.appActive[r.app]--
	} else {
		delete(a.appActive, r.app)
	}
	cost := r.cost
	status := "unavailable"
	if usage != nil {
		status = "reported"
	}
	if r.priced && usage != nil {
		price := a.limits.Pricing[r.alias]
		cost = estimateAICost(price, usage.PromptTokens, usage.CompletionTokens)
		if a.day == r.day {
			a.globalSpend += cost - r.cost
			a.appSpend[r.app] += cost - r.cost
		}
	}
	if !r.priced {
		return 0, status
	}
	return cost, status
}

func (a *aiAdmission) reject(reason string) {
	// Admission logs use only a bounded reason code, never app-controlled values.
	log.Printf("AI admission rejected reason=%q", reason)
}
