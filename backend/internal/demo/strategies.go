package demo

import (
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

func f64(v float64) *float64 { return &v }

// strategy registry — static demo definitions with parameter schemas the
// frontend renders dynamically.
var strategies = []strategy.Definition{
	{
		ID: "ema-cross", Name: "EMA Crossover", Version: "1.2.0",
		Description:     "Trend-following system that enters long on EMA20/EMA50 bullish crosses and shorts bearish crosses, with an ATR-based stop.",
		SupportedAssets: []string{"xauusd"},
		Timeframe:       market.Timeframe1h,
		Status:          "active",
		Parameters: []strategy.ParameterSpec{
			{Name: "fastLength", Label: "Fast EMA", Type: "number", Default: 20.0, Min: f64(2), Max: f64(100), Step: f64(1)},
			{Name: "slowLength", Label: "Slow EMA", Type: "number", Default: 50.0, Min: f64(10), Max: f64(300), Step: f64(1)},
			{Name: "stopLossPct", Label: "Stop loss %", Type: "number", Default: 1.5, Min: f64(0.1), Max: f64(10), Step: f64(0.1)},
			{Name: "direction", Label: "Direction", Type: "select", Default: "both", Options: []string{"both", "long-only", "short-only"}},
		},
	},
	{
		ID: "rsi-reversion", Name: "RSI Mean Reversion", Version: "2.0.1",
		Description:     "Fades overextended RSI readings back toward the mean. Long below the lower band, short above the upper band.",
		SupportedAssets: []string{"xauusd"},
		Timeframe:       market.Timeframe15m,
		Status:          "active",
		Parameters: []strategy.ParameterSpec{
			{Name: "rsiLength", Label: "RSI length", Type: "number", Default: 14.0, Min: f64(2), Max: f64(50), Step: f64(1)},
			{Name: "oversold", Label: "Oversold level", Type: "number", Default: 30.0, Min: f64(5), Max: f64(45), Step: f64(1)},
			{Name: "overbought", Label: "Overbought level", Type: "number", Default: 70.0, Min: f64(55), Max: f64(95), Step: f64(1)},
			{Name: "useTrendFilter", Label: "Trend filter", Type: "boolean", Default: true},
		},
	},
	{
		ID: "atr-breakout", Name: "ATR Breakout", Version: "0.9.0",
		Description:     "Enters on range breakouts confirmed by ATR expansion; trails stops with a chandelier exit.",
		SupportedAssets: []string{"xauusd"},
		Timeframe:       market.Timeframe4h,
		Status:          "inactive",
		Parameters: []strategy.ParameterSpec{
			{Name: "atrLength", Label: "ATR length", Type: "number", Default: 14.0, Min: f64(5), Max: f64(50), Step: f64(1)},
			{Name: "breakoutMult", Label: "Breakout mult", Type: "number", Default: 2.0, Min: f64(0.5), Max: f64(6), Step: f64(0.1)},
			{Name: "trailMult", Label: "Trail mult", Type: "number", Default: 3.0, Min: f64(1), Max: f64(8), Step: f64(0.1)},
		},
	},
}

func strategyByID(id string) (strategy.Definition, bool) {
	for _, s := range strategies {
		if s.ID == id {
			return s, true
		}
	}
	return strategy.Definition{}, false
}
