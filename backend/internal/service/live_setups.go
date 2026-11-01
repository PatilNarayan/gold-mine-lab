package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/gold-mine-lab/backend/internal/tradeplan"
)

// SetupStatus is the evaluator's per-strategy outcome.
type SetupStatus string

const (
	SetupActionable   SetupStatus = "actionable"        // BUY + plan + fresh entry quote
	SetupPlanRejected SetupStatus = "plan_rejected"     // BUY but tradeplan rejected — see RejectionReason
	SetupLongExit     SetupStatus = "long_exit"         // SELL — exit signal, never a short plan
	SetupNoSetup      SetupStatus = "no_setup"          // NEUTRAL
	SetupInsufficient SetupStatus = "insufficient_data" // not enough completed candles
	SetupStale        SetupStatus = "stale"             // quote older than the provider staleness window
	SetupUnavailable  SetupStatus = "unavailable"       // provider disabled/disconnected/errored
)

// SetupEvaluation is one strategy's latest live evaluation. When Plan is
// non-nil it is the tradeplan.Plan produced by Phase 20A — never a copy.
type SetupEvaluation struct {
	StrategyID       string           `json:"strategyId"`
	StrategyVersion  string           `json:"strategyVersion"`
	Timeframe        market.Timeframe `json:"timeframe"`
	Signal           *strategy.Signal `json:"signal,omitempty"`
	SignalCandleTime *time.Time       `json:"signalCandleTime,omitempty"`
	EvaluatedAt      time.Time        `json:"evaluatedAt"`
	Direction        string           `json:"direction,omitempty"`
	EntryPrice       *float64         `json:"entryPrice,omitempty"`
	EntryPriceSource string           `json:"entryPriceSource,omitempty"` // "ask" | "mid" | "last" | "signal_close" | ""
	Plan             *tradeplan.Plan  `json:"plan,omitempty"`
	RiskReward       *float64         `json:"riskReward,omitempty"` // (T2-Entry)/Risk — the standard primary-target RR
	Status           SetupStatus      `json:"status"`
	RejectionReason  string           `json:"rejectionReason,omitempty"`
}

// LiveSetupEvaluator evaluates registered strategies against the provider's
// latest COMPLETED candles and converts BUY signals into trade plans. It is
// read-only and deterministic: identical inputs yield identical outputs, and
// nothing is persisted.
type LiveSetupEvaluator struct {
	provider live.Provider
	registry strategy.Registry
	cfg      tradeplan.Config
	now      func() time.Time // injectable for tests
}

func NewLiveSetupEvaluator(p live.Provider, reg strategy.Registry, cfg tradeplan.Config) *LiveSetupEvaluator {
	return &LiveSetupEvaluator{provider: p, registry: reg, cfg: cfg, now: func() time.Time { return time.Now().UTC() }}
}

// tfDuration maps supported timeframes to their bar length in label space.
// 1d is handled separately: its boundary is the Kyiv session day, not +24h.
var tfDuration = map[market.Timeframe]time.Duration{
	market.Timeframe1m: time.Minute, market.Timeframe5m: 5 * time.Minute,
	market.Timeframe15m: 15 * time.Minute, market.Timeframe1h: time.Hour,
	market.Timeframe4h: 4 * time.Hour,
}

// completedCandles drops any candle whose bar period has not fully closed in
// broker-time label space. The provider's book can end with a forming bar
// assembled from streamed quotes — that bar must never produce a signal.
func completedCandles(cs []live.Candle, tf market.Timeframe, nowLabel time.Time) []live.Candle {
	if len(cs) == 0 {
		return nil
	}
	if tf == market.Timeframe1d {
		// A daily candle is complete only if its session day precedes today's.
		dayStart := time.Date(nowLabel.Year(), nowLabel.Month(), nowLabel.Day(), 0, 0, 0, 0, nowLabel.Location())
		end := len(cs)
		for end > 0 && !cs[end-1].Time.Before(dayStart) {
			end--
		}
		return cs[:end]
	}
	d, ok := tfDuration[tf]
	if !ok {
		return nil
	}
	end := len(cs)
	for end > 0 && cs[end-1].Time.Add(d).After(nowLabel) {
		end--
	}
	return cs[:end]
}

// entryFromQuote resolves the entry reference honestly: a real ask when the
// provider supplies one, else the streamed price labeled by its actual kind
// (mid is never presented as an executable ask), else the signal candle's
// close — flagged so callers know it is not a live quote.
func entryFromQuote(q live.Quote, hasQuote bool, signalClose float64) (float64, string) {
	if hasQuote {
		if q.Ask != nil && *q.Ask > 0 {
			return *q.Ask, "ask"
		}
		if q.Price > 0 {
			return q.Price, q.PriceKind // "mid" | "last" — semantics preserved
		}
	}
	return signalClose, "signal_close"
}

// Evaluate runs every registered strategy once against its default timeframe.
// Slice order is registry order — deterministic per call.
func (e *LiveSetupEvaluator) Evaluate(ctx context.Context) []SetupEvaluation {
	evaluatedAt := e.now()
	nowLabel := live.NowLabel()
	out := make([]SetupEvaluation, 0, len(e.registry.List()))
	for _, st := range e.registry.List() {
		out = append(out, e.evalOne(ctx, st, evaluatedAt, nowLabel))
	}
	return out
}

func (e *LiveSetupEvaluator) evalOne(ctx context.Context, st strategy.Strategy, evaluatedAt, nowLabel time.Time) SetupEvaluation {
	res := SetupEvaluation{
		StrategyID:      st.ID(),
		StrategyVersion: st.Version(),
		Timeframe:       st.Timeframe(),
		EvaluatedAt:     evaluatedAt,
		Status:          SetupUnavailable,
	}
	if e.provider == nil {
		res.RejectionReason = "live provider is disabled"
		return res
	}
	tf := st.Timeframe()
	if !liveTimeframes[tf] {
		res.RejectionReason = fmt.Sprintf("timeframe %q not supported by live provider", tf)
		return res
	}

	raw, err := e.provider.Candles(ctx, string(tf), 0)
	if err != nil {
		res.RejectionReason = fmt.Sprintf("provider candle fetch failed: %v", err)
		return res
	}
	closed := completedCandles(raw, tf, nowLabel)
	if len(closed) == 0 {
		res.Status = SetupInsufficient
		res.RejectionReason = "no completed candles — only a forming bar or empty book"
		return res
	}

	mcs := make([]market.Candle, len(closed))
	for i, c := range closed {
		mcs[i] = market.Candle{Time: c.Time, Open: c.Open, High: c.High, Low: c.Low, Close: c.Close}
		if c.Volume != nil {
			mcs[i].Volume = *c.Volume
		}
	}

	sig, err := st.Evaluate(ctx, mcs)
	if err != nil {
		res.RejectionReason = fmt.Sprintf("strategy evaluation failed: %v", err)
		return res
	}
	res.Signal = &sig
	t := closed[len(closed)-1].Time
	res.SignalCandleTime = &t

	if sig.Action != strategy.ActionBuy {
		if sig.Action == strategy.ActionSell {
			res.Status = SetupLongExit // long-exit marker — never a short plan
		} else {
			res.Status = SetupNoSetup
		}
		return res
	}

	// Entry reference: fresh live ask > streamed price (kind-labeled) >
	// signal close. Quote freshness is judged on its provider timestamp
	// (falling back to receipt time) against the provider's staleness window.
	q, hasQuote := e.provider.LatestQuote()
	pstat := e.provider.Status()
	fresh := false
	if hasQuote {
		qt := q.ReceivedAt
		if q.ProviderTime != nil {
			qt = *q.ProviderTime
		}
		fresh = e.now().Sub(qt) <= pstat.StaleAfter
	}

	entry, src := entryFromQuote(q, hasQuote && fresh, sig.Price)
	res.EntryPrice = &entry
	res.EntryPriceSource = src

	plan, rej := tradeplan.Calculate(tradeplan.Input{
		Signal: sig, Entry: entry, Candles: mcs,
	}, e.cfg)
	if rej != nil {
		res.Status = SetupPlanRejected
		res.RejectionReason = fmt.Sprintf("%s: %s", rej.Reason, rej.Detail)
		return res
	}
	res.Plan = plan
	res.Direction = plan.Direction
	rr := (plan.T2 - plan.Entry) / plan.RiskPerUnit
	res.RiskReward = &rr

	if !hasQuote {
		res.Status = SetupStale
		res.RejectionReason = "no live quote — plan computed on signal close; setup is not currently executable"
		return res
	}
	if !fresh {
		res.Status = SetupStale
		res.RejectionReason = "live quote older than the provider staleness window; setup is not currently executable"
		return res
	}
	res.Status = SetupActionable
	return res
}
