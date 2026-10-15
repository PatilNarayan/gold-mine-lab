package service

import (
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
)

func d(s string) time.Time {
	t, _ := time.Parse("2006-01-02", s)
	return t
}

func TestGenerateFolds(t *testing.T) {
	// 7y range, 2y IS + 1y OOS, step=OOS: fold i needs i*12+36 ≤ 84 months
	// → folds starting 2010..2014 = 5.
	folds := generateFolds(d("2010-01-01"), d("2017-01-01"), 24, 12, 12)
	if len(folds) != 5 {
		t.Fatalf("got %d folds, want 5", len(folds))
	}
	f0 := folds[0]
	if f0.IsStart != d("2010-01-01") || f0.IsEnd != d("2012-01-01") ||
		f0.OosStart != d("2012-01-01") || f0.OosEnd != d("2013-01-01") {
		t.Errorf("fold0 boundaries wrong: %+v", f0)
	}
	f1 := folds[1]
	if f1.OosStart != d("2013-01-01") || f1.OosEnd != d("2014-01-01") {
		t.Errorf("fold1 OOS wrong: %+v", f1)
	}
	// OOS never overlaps: next fold's OOS starts at/after previous OOS end.
	for i := 1; i < len(folds); i++ {
		if folds[i].OosStart.Before(folds[i-1].OosEnd) {
			t.Fatalf("OOS windows overlap: %v vs %v", folds[i-1], folds[i])
		}
	}
	// A partial tail is dropped, not silently shortened: range through
	// mid-2016 fits 4 folds (last OOS ends 2016-01); the 5th's OOS
	// (ending 2017-01) doesn't fit.
	folds = generateFolds(d("2010-01-01"), d("2016-06-30"), 24, 12, 12)
	if len(folds) != 4 {
		t.Fatalf("partial tail must be dropped, got %d folds", len(folds))
	}
	// Range shorter than one full fold → empty (rejected by validation).
	if got := generateFolds(d("2020-01-01"), d("2021-06-01"), 12, 12, 12); len(got) != 0 {
		t.Fatalf("expected 0 folds, got %d", len(got))
	}
	// Every OOS window is strictly after its IS window by construction.
	for _, f := range folds {
		if !f.OosStart.Equal(f.IsEnd) || !f.OosEnd.After(f.OosStart) {
			t.Errorf("bad OOS adjacency: %+v", f)
		}
	}
}

func TestValidateWalkForwardRules(t *testing.T) {
	s := &Service{}
	mk := func() (r api.WalkForwardRequest) {
		return api.WalkForwardRequest{
			Legs:    []api.ComparisonLeg{{StrategyID: "ema-cross"}, {StrategyID: "rsi-mean-reversion"}},
			AssetID: "xauusd", Timeframe: "1d",
			StartDate: "2010-01-01", EndDate: "2020-01-01",
			InSampleMonths: 24, OosMonths: 12, StepMonths: 12,
			InitialCapital: 10000, PositionSize: 5,
		}
	}
	if _, err := s.validateWalkForward(mk()); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := map[string]func(*api.WalkForwardRequest){
		"no legs":      func(r *api.WalkForwardRequest) { r.Legs = nil },
		"dup strategy": func(r *api.WalkForwardRequest) { r.Legs[1].StrategyID = "ema-cross" },
		"end<=start":   func(r *api.WalkForwardRequest) { r.EndDate = "2009-01-01" },
		"is=0":         func(r *api.WalkForwardRequest) { r.InSampleMonths = 0 },
		"oos=0":        func(r *api.WalkForwardRequest) { r.OosMonths = 0 },
		"step<oos":     func(r *api.WalkForwardRequest) { r.StepMonths = 6 },
		"no fold fits": func(r *api.WalkForwardRequest) { r.EndDate = "2011-06-01" },
		"bad date":     func(r *api.WalkForwardRequest) { r.StartDate = "not-a-date" },
	}
	for name, mutate := range cases {
		r := mk()
		mutate(&r)
		if _, err := s.validateWalkForward(r); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
	// step defaults to OOS length.
	r := mk()
	r.StepMonths = 0
	folds, err := s.validateWalkForward(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(folds) != 8 { // 10y range, 3y/fold, step 1y → folds start 2010..2017
		t.Fatalf("got %d folds, want 8", len(folds))
	}
}
