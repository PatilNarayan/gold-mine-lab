package demo

import "github.com/gold-mine-lab/backend/internal/market"

// assetMeta drives the synthetic candle generator; it is not part of the API
// Asset shape.
type assetMeta struct {
	basePrice  float64
	digits     int
	volatility float64 // per-bar stdev as a fraction of price
	baseVolume float64
}

// assets is what the API serves. Focused on XAUUSD — the only asset with a
// real dataset loaded. More symbols become available as their CSVs arrive;
// the schema and UI stay asset-agnostic.
var assets = []market.Asset{
	{ID: "xauusd", Symbol: "XAUUSD", Name: "Gold Spot", AssetClass: "metal", QuoteCurrency: "USD"},
}

var assetMetas = map[string]assetMeta{
	"xauusd": {basePrice: 2320, digits: 2, volatility: 0.0075, baseVolume: 185_000},
	"xagusd": {basePrice: 29.4, digits: 3, volatility: 0.011, baseVolume: 62_000},
	"msft":   {basePrice: 428, digits: 2, volatility: 0.009, baseVolume: 4_200_000},
	"tsla":   {basePrice: 246, digits: 2, volatility: 0.019, baseVolume: 18_500_000},
	"eurusd": {basePrice: 1.0852, digits: 4, volatility: 0.0028, baseVolume: 310_000},
}

func assetByID(id string) (market.Asset, bool) {
	for _, a := range assets {
		if a.ID == id {
			return a, true
		}
	}
	return market.Asset{}, false
}

func meta(id string) assetMeta {
	if m, ok := assetMetas[id]; ok {
		return m
	}
	return assetMeta{basePrice: 100, digits: 2, volatility: 0.01, baseVolume: 100_000}
}
