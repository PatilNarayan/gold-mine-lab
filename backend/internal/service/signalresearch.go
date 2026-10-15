package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/regime"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// Signal research (Phase 9): replay several candidates (built-ins and/or
// saved graph revisions) over ONE shared candle sequence and report
// per-candidate stats, pairwise agreement/conflict, and an optional
// forward price-movement study for BUY episodes.
//
// Signal generation and outcome attribution are strictly separated:
// every signal is produced without future candles; the forward study reads
// later candles only afterwards and can never feed back into evaluation.
// No trades are executed and no per-candle signals are persisted — only
// summary statistics.

const (
	srMaxLegs     = 8
	srMaxEvalBars = 250_000
)

var srHorizons = []int{1, 5, 10, 20}

func (s *Service) ExecuteSignalResearch(ctx context.Context, req api.SignalResearchRequest) (api.SignalResearch, error) {
	verr := func(m string) (api.SignalResearch, error) {
		return api.SignalResearch{}, &api.ValidationError{Msg: m}
	}
	if len(req.Legs) < 2 {
		return verr("at least 2 candidates are required")
	}
	if len(req.Legs) > srMaxLegs {
		return verr(fmt.Sprintf("at most %d candidates", srMaxLegs))
	}
	if _, ok := serveCaps[req.Timeframe]; !ok {
		return verr("unsupported timeframe")
	}
	start, e1 := time.Parse("2006-01-02", req.StartDate)
	end, e2 := time.Parse("2006-01-02", req.EndDate)
	if e1 != nil || e2 != nil || !end.After(start) {
		return verr("invalid startDate/endDate")
	}
	asset, err := s.repos.Assets.GetAsset(ctx, req.AssetID)
	if errors.Is(err, repository.ErrNotFound) {
		return verr("unknown assetId")
	}
	if err != nil {
		return api.SignalResearch{}, err
	}

	// Resolve + configure each candidate; graphs come from the saved
	// immutable revision (params rejected), built-ins take client params.
	seen := map[string]bool{}
	streams := make([]strategy.Stream, len(req.Legs))
	defs := make([]strategy.Definition, len(req.Legs))
	maxWarmup := 0
	for i, leg := range req.Legs {
		if seen[leg.StrategyID] {
			return verr(fmt.Sprintf("duplicate candidate %s", leg.StrategyID))
		}
		seen[leg.StrategyID] = true
		st, err := s.resolveStrategy(ctx, leg.StrategyID)
		if err != nil {
			var ve *api.ValidationError
			if errors.As(err, &ve) {
				return verr(ve.Msg)
			}
			if errors.Is(err, repository.ErrNotFound) {
				return verr("unknown strategyId")
			}
			return api.SignalResearch{}, err
		}
		if st == nil {
			return verr("unknown strategyId")
		}
		params, err := graphLegParameters(leg.StrategyID, leg.Parameters)
		if err != nil {
			var ve *api.ValidationError
			if errors.As(err, &ve) {
				return verr(ve.Msg)
			}
			return api.SignalResearch{}, err
		}
		if cfg, ok := st.(strategy.Configurable); ok && len(params) > 0 && !isGraphSlug(leg.StrategyID) {
			st, err = cfg.Configure(params)
			if err != nil {
				return verr(err.Error())
			}
		}
		def := st.(interface{ Describe() strategy.Definition }).Describe()
		supportsAsset, supportsTF := false, false
		for _, a := range def.SupportedAssets {
			if a == req.AssetID {
				supportsAsset = true
			}
		}
		for _, t := range def.SupportedTimeframes {
			if t == req.Timeframe {
				supportsTF = true
			}
		}
		if !supportsAsset {
			return verr(fmt.Sprintf("candidate %s does not support asset %s", leg.StrategyID, req.AssetID))
		}
		if !supportsTF {
			return verr(fmt.Sprintf("candidate %s does not support timeframe %s", leg.StrategyID, req.Timeframe))
		}
		streamer, ok := st.(strategy.Streamer)
		if !ok {
			return verr(fmt.Sprintf("candidate %s does not support streaming evaluation", leg.StrategyID))
		}
		streams[i] = streamer.NewStream()
		defs[i] = def
		if w, ok := st.(strategy.Warmupper); ok && w.WarmupBars() > maxWarmup {
			maxWarmup = w.WarmupBars()
		}
	}

	// Regime classifiers share the same candle prefix; the research warm-up
	// must cover them too (EMA-200 dominates at 200 bars).
	if req.RegimeAnalysis && maxWarmup < regime.WarmupBars {
		maxWarmup = regime.WarmupBars
	}
	to := end.AddDate(0, 0, 1)
	candles, err := s.data.Range(asset, req.Timeframe, start, to, maxWarmup)
	if err != nil {
		return verr(fmt.Sprintf("no candle data for window: %v", err))
	}

	// Provenance is established up front: an analysis can never be
	// persisted without identifying the exact dataset it ran on.
	fp, err := s.data.Fingerprint(asset, req.Timeframe)
	if err != nil {
		return api.SignalResearch{}, fmt.Errorf("dataset fingerprint unavailable for %s %s: %w", req.AssetID, req.Timeframe, err)
	}
	if fp == "" {
		return api.SignalResearch{}, fmt.Errorf("dataset fingerprint empty for %s %s", req.AssetID, req.Timeframe)
	}
	windowStart := 0
	for windowStart < len(candles) && candles[windowStart].Time.Before(start) {
		windowStart++
	}
	evalBars := len(candles) - windowStart
	if evalBars < 1 {
		return verr("no candles inside the requested window")
	}
	if evalBars > srMaxEvalBars {
		return verr(fmt.Sprintf("window evaluates %d candles, exceeding the %d limit — narrow the date range", evalBars, srMaxEvalBars))
	}

	// Persist the request so a crash leaves an honest pending/failed row.
	reqJSON, _ := json.Marshal(req)
	rowID, err := s.repos.SignalResearch.CreateSignalResearch(ctx, reqJSON)
	if err != nil {
		return api.SignalResearch{}, err
	}

	// Replay: every candidate consumes the SAME chronological candles once.
	actions := make([][]strategy.Action, len(streams))
	scores := make([][]float64, len(streams))
	for i := range streams {
		actions[i] = make([]strategy.Action, evalBars)
		scores[i] = make([]float64, evalBars)
	}
	var cls *regime.Classifier
	var trendLbls, volLbls []string
	if req.RegimeAnalysis {
		cls = regime.New()
		trendLbls = make([]string, evalBars)
		volLbls = make([]string, evalBars)
	}
	for ci, c := range candles {
		if err := ctx.Err(); err != nil {
			s.failSignalResearch(rowID, fmt.Errorf("signal research cancelled: %w", err))
			return api.SignalResearch{}, err
		}
		wi := ci - windowStart
		if cls != nil {
			tl, vl := cls.Next(c)
			if wi >= 0 {
				trendLbls[wi], volLbls[wi] = string(tl), string(vl)
			}
		}
		for li, st := range streams {
			sig := st.Next(c)
			if wi >= 0 {
				actions[li][wi] = sig.Action
				scores[li][wi] = sig.Score
			}
		}
	}

	// Per-candidate stats + episodes.
	candStats := make([]api.SignalCandidateStats, len(streams))
	buyEpisodes := make([][]int, len(streams)) // first-bar index of each BUY episode
	for i := range streams {
		cs := api.SignalCandidateStats{StrategyID: defs[i].ID, Version: defs[i].Version, EvaluatedBars: evalBars}
		var sc float64
		prev := strategy.ActionNeutral
		for wi := 0; wi < evalBars; wi++ {
			a := actions[i][wi]
			switch a {
			case strategy.ActionBuy:
				cs.Buy++
				sc += scores[i][wi]
			case strategy.ActionSell:
				cs.Sell++
				sc += scores[i][wi]
			default:
				cs.Neutral++
			}
			if a == strategy.ActionBuy && prev != strategy.ActionBuy {
				cs.BuyEpisodes++
				buyEpisodes[i] = append(buyEpisodes[i], wi)
			}
			if a == strategy.ActionSell && prev != strategy.ActionSell {
				cs.SellEpisodes++
			}
			prev = a
		}
		act := cs.Buy + cs.Sell
		cs.ActionablePct = pct(act, evalBars)
		if act > 0 {
			v := sc / float64(act)
			cs.AvgScore = &v
		}
		if req.RegimeAnalysis {
			cs.ByTrend = srRegimeStats(trendLbls, actions[i], scores[i], trendOrder)
			cs.ByVolatility = srRegimeStats(volLbls, actions[i], scores[i], volOrder)
		}
		candStats[i] = cs
	}

	// Pairwise agreement over shared evaluated bars (same candle indexes).
	var pairs []api.SignalPairAgreement
	for i := 0; i < len(streams); i++ {
		for j := i + 1; j < len(streams); j++ {
			p := api.SignalPairAgreement{A: defs[i].ID, B: defs[j].ID}
			for wi := 0; wi < evalBars; wi++ {
				a, b := actions[i][wi], actions[j][wi]
				aAct := a == strategy.ActionBuy || a == strategy.ActionSell
				bAct := b == strategy.ActionBuy || b == strategy.ActionSell
				switch {
				case aAct && bAct:
					p.CoActionable++
					if a == b {
						p.SameDirection++
						if a == strategy.ActionBuy {
							p.BothBuy++
						} else {
							p.BothSell++
						}
					} else {
						p.Opposite++
					}
				case aAct != bAct:
					p.OneActionable++
				default:
					p.BothNeutral++
				}
			}
			if p.CoActionable > 0 {
				ag := pct(p.SameDirection, p.CoActionable)
				cf := pct(p.Opposite, p.CoActionable)
				p.AgreementPct, p.ConflictPct = &ag, &cf
			}
			if req.RegimeAnalysis {
				p.ByTrend = srRegimeAgreement(trendLbls, actions[i], actions[j], trendOrder)
				p.ByVolatility = srRegimeAgreement(volLbls, actions[i], actions[j], volOrder)
			}
			pairs = append(pairs, p)
		}
	}

	// Optional forward study on BUY episodes: event = first BUY bar,
	// entry = next candle open, exit = close h bars after the signal.
	// Horizons requiring unavailable future candles are excluded, never
	// shortened. These are gross price moves, not P&L.
	var forward []api.SignalForwardResult
	if req.EventStudy {
		for i := range streams {
			fr := api.SignalForwardResult{StrategyID: defs[i].ID}
			for _, h := range srHorizons {
				hr := api.SignalHorizonResult{Horizon: h, StrategyID: defs[i].ID}
				var rets []float64
				for _, ev := range buyEpisodes[i] {
					gi := windowStart + ev // index into candles
					entryIdx, exitIdx := gi+1, gi+h
					if exitIdx >= len(candles) || entryIdx >= len(candles) {
						hr.Excluded++
						continue
					}
					entry, exit := candles[entryIdx].Open, candles[exitIdx].Close
					if entry <= 0 || math.IsNaN(entry) || math.IsNaN(exit) {
						hr.Excluded++
						continue
					}
					rets = append(rets, (exit-entry)/entry*100)
				}
				hr.Eligible = len(rets)
				if len(rets) > 0 {
					mn := mean(rets)
					md := median(rets)
					pos := pct(posCount(rets), len(rets))
					wr := minF(rets)
					hr.MeanReturn, hr.MedianReturn, hr.PositivePct, hr.WorstReturn = &mn, &md, &pos, &wr
				}
				fr.Horizons = append(fr.Horizons, hr)
			}
			if req.RegimeAnalysis {
				fr.ByTrend = srRegimeForward(trendLbls, buyEpisodes[i], windowStart, candles, trendOrder)
				fr.ByVolatility = srRegimeForward(volLbls, buyEpisodes[i], windowStart, candles, volOrder)
			}
			forward = append(forward, fr)
		}
	}

	out := api.SignalResearch{
		ID: rowID, Status: "completed", Name: req.Name,
		AssetID: req.AssetID, Timeframe: req.Timeframe,
		StartDate: req.StartDate, EndDate: req.EndDate, EventStudy: req.EventStudy,
		EvaluatedBars: evalBars, WarmupBars: maxWarmup,
		Candidates: candStats, Pairs: pairs, Forward: forward,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, l := range req.Legs {
		out.StrategyIDs = append(out.StrategyIDs, l.StrategyID)
	}
	out.Dataset = &fp
	if req.RegimeAnalysis {
		rv := regime.DefinitionVersion
		out.RegimeVersion = &rv
	}
	lc := candles[len(candles)-1].Time.UTC().Format("2006-01-02T15:04:05Z")
	out.LastCandle = &lc

	resJSON, _ := json.Marshal(out)
	if err := s.repos.SignalResearch.SetSignalResearchResult(ctx, rowID, "completed", resJSON, nil); err != nil {
		return api.SignalResearch{}, err
	}
	return out, nil
}

func (s *Service) ListSignalResearch(ctx context.Context) ([]api.SignalResearch, error) {
	rows, err := s.repos.SignalResearch.ListSignalResearch(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.SignalResearch, 0, len(rows))
	for _, r := range rows {
		sr, err := signalResearchFromRow(r, true)
		if err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, nil
}

func (s *Service) GetSignalResearch(ctx context.Context, id string) (api.SignalResearch, error) {
	r, err := s.repos.SignalResearch.GetSignalResearch(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return api.SignalResearch{}, api.ErrNotFound
	}
	if err != nil {
		return api.SignalResearch{}, err
	}
	return signalResearchFromRow(r, false)
}

// signalResearchFromRow rebuilds the response. For completed rows the stored
// result JSONB is returned as-is (minus candidates/pairs/forward in list
// view); for pending/failed rows the request supplies the summary fields.
// Persisted JSON that cannot be decoded is an integrity error, never a
// silently zero-valued response.
func signalResearchFromRow(r repository.SignalResearchRow, summaryOnly bool) (api.SignalResearch, error) {
	var out api.SignalResearch
	if len(r.Result) > 0 {
		if err := json.Unmarshal(r.Result, &out); err != nil {
			return api.SignalResearch{}, fmt.Errorf("signal research %s: stored result is malformed: %w", r.ID, err)
		}
	} else if r.Status == "completed" {
		return api.SignalResearch{}, fmt.Errorf("signal research %s: completed record has no stored result", r.ID)
	}
	if len(out.StrategyIDs) == 0 && len(r.Request) > 0 {
		var req api.SignalResearchRequest
		if err := json.Unmarshal(r.Request, &req); err != nil {
			return api.SignalResearch{}, fmt.Errorf("signal research %s: stored request is malformed: %w", r.ID, err)
		}
		out.AssetID, out.Timeframe = req.AssetID, req.Timeframe
		out.StartDate, out.EndDate = req.StartDate, req.EndDate
		out.EventStudy, out.Name = req.EventStudy, req.Name
		for _, l := range req.Legs {
			out.StrategyIDs = append(out.StrategyIDs, l.StrategyID)
		}
	}
	if out.StrategyIDs == nil {
		out.StrategyIDs = []string{}
	}
	out.ID, out.Status, out.Error = r.ID, r.Status, r.Error
	out.CreatedAt = r.CreatedAt.UTC().Format(time.RFC3339)
	if summaryOnly {
		out.Candidates, out.Pairs, out.Forward = nil, nil, nil
	}
	return out, nil
}

// failSignalResearch best-effort transitions a persisted analysis to failed
// on a bounded context detached from the (possibly cancelled) request. A
// cleanup failure is logged separately and never masks the original cause.
func (s *Service) failSignalResearch(rowID string, cause error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 10*time.Second)
	defer cancel()
	msg := cause.Error()
	if err := s.repos.SignalResearch.SetSignalResearchResult(cctx, rowID, "failed", nil, &msg); err != nil {
		s.log.Error("signal research: could not persist failed status", "id", rowID, "cause", msg, "error", err.Error())
	}
}

func isGraphSlug(id string) bool { return len(id) > 6 && id[:6] == "graph:" }

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d) * 100
}

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func posCount(v []float64) int {
	n := 0
	for _, x := range v {
		if x > 0 {
			n++
		}
	}
	return n
}

func minF(v []float64) float64 {
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// --- regime-conditioned summaries -----------------------------------------

var (
	trendOrder = []string{string(regime.TrendUp), string(regime.TrendDown), string(regime.TrendTransition), string(regime.TrendUnknown)}
	volOrder   = []string{string(regime.VolLow), string(regime.VolNormal), string(regime.VolHigh), string(regime.VolUnknown)}
)

// srRegimeStats aggregates per-candidate signal stats inside each regime
// bucket. Episodes follow the same convention as the overall stats: a
// consecutive run of the same actionable direction within the bucket's own
// chronological subsequence.
func srRegimeStats(labels []string, acts []strategy.Action, scores []float64, order []string) []api.SignalRegimeStats {
	type acc struct {
		st   api.SignalRegimeStats
		sum  float64
		prev strategy.Action
	}
	m := map[string]*acc{}
	for _, r := range order {
		m[r] = &acc{st: api.SignalRegimeStats{Regime: r}}
	}
	for wi, rg := range labels {
		b := m[rg]
		b.st.EvaluatedBars++
		switch a := acts[wi]; a {
		case strategy.ActionBuy:
			b.st.Buy++
			b.sum += scores[wi]
			if b.prev != strategy.ActionBuy {
				b.st.BuyEpisodes++
			}
		case strategy.ActionSell:
			b.st.Sell++
			b.sum += scores[wi]
			if b.prev != strategy.ActionSell {
				b.st.SellEpisodes++
			}
		default:
			b.st.Neutral++
		}
		b.prev = acts[wi]
	}
	out := make([]api.SignalRegimeStats, 0, len(order))
	for _, r := range order {
		b := m[r]
		a := b.st.Buy + b.st.Sell
		if b.st.EvaluatedBars > 0 {
			b.st.ActionablePct = pct(a, b.st.EvaluatedBars)
		}
		if a > 0 {
			v := b.sum / float64(a)
			b.st.AvgScore = &v
		}
		out = append(out, b.st)
	}
	return out
}

// srRegimeAgreement is the pairwise agreement computation restricted to the
// bars of each regime bucket (same definitions as the overall pass).
func srRegimeAgreement(labels []string, actA, actB []strategy.Action, order []string) []api.SignalRegimeAgreement {
	m := map[string]*api.SignalRegimeAgreement{}
	for _, r := range order {
		m[r] = &api.SignalRegimeAgreement{Regime: r}
	}
	for wi, rg := range labels {
		p := m[rg]
		a, b := actA[wi], actB[wi]
		aAct := a == strategy.ActionBuy || a == strategy.ActionSell
		bAct := b == strategy.ActionBuy || b == strategy.ActionSell
		switch {
		case aAct && bAct:
			p.CoActionable++
			if a == b {
				p.SameDirection++
				if a == strategy.ActionBuy {
					p.BothBuy++
				} else {
					p.BothSell++
				}
			} else {
				p.Opposite++
			}
		case aAct != bAct:
			p.OneActionable++
		default:
			p.BothNeutral++
		}
	}
	out := make([]api.SignalRegimeAgreement, 0, len(order))
	for _, r := range order {
		p := m[r]
		if p.CoActionable > 0 {
			ag := pct(p.SameDirection, p.CoActionable)
			cf := pct(p.Opposite, p.CoActionable)
			p.AgreementPct, p.ConflictPct = &ag, &cf
		}
		out = append(out, *p)
	}
	return out
}

// srRegimeForward groups the BUY-episode forward study by the regime at each
// episode's signal candle (the regime never changes after the fact, even
// though outcomes read later candles).
func srRegimeForward(labels []string, episodes []int, windowStart int, candles []market.Candle, order []string) []api.SignalRegimeForward {
	type bucket struct {
		rets map[int][]float64
		excl map[int]int
	}
	m := map[string]*bucket{}
	for _, r := range order {
		m[r] = &bucket{rets: map[int][]float64{}, excl: map[int]int{}}
	}
	for _, ev := range episodes {
		b := m[labels[ev]]
		gi := windowStart + ev
		for _, h := range srHorizons {
			entryIdx, exitIdx := gi+1, gi+h
			if exitIdx >= len(candles) || entryIdx >= len(candles) {
				b.excl[h]++
				continue
			}
			entry, exit := candles[entryIdx].Open, candles[exitIdx].Close
			if entry <= 0 || math.IsNaN(entry) || math.IsNaN(exit) {
				b.excl[h]++
				continue
			}
			b.rets[h] = append(b.rets[h], (exit-entry)/entry*100)
		}
	}
	out := make([]api.SignalRegimeForward, 0, len(order))
	for _, r := range order {
		srf := api.SignalRegimeForward{Regime: r}
		for _, h := range srHorizons {
			rets := m[r].rets[h]
			hr := api.SignalHorizonResult{Horizon: h, Eligible: len(rets), Excluded: m[r].excl[h]}
			if len(rets) > 0 {
				mn := mean(rets)
				md := median(rets)
				pos := pct(posCount(rets), len(rets))
				wr := minF(rets)
				hr.MeanReturn, hr.MedianReturn, hr.PositivePct, hr.WorstReturn = &mn, &md, &pos, &wr
			}
			srf.Horizons = append(srf.Horizons, hr)
		}
		out = append(out, srf)
	}
	return out
}
