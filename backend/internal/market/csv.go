package market

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CSVProvider is a DataProvider backed by historical CSV exports (e.g. the
// Kaggle XAUUSD dataset). Files live in a data directory as
// "<PREFIX>_<tf>_data.csv" with rows "2004.06.11 00:00;384;384.8;382.8;384.1;272".
// Timestamps are treated as UTC.
//
// To bound memory, only a tail window per timeframe is retained (the rows the
// app actually serves). The 1m file is ~350 MB — streaming keeps RSS small but
// the first 1m request still pays the scan.
type CSVProvider struct {
	dir      string
	tailKeep map[Timeframe]int

	mu      sync.Mutex
	loaded  map[string][]Candle // key: "PREFIX|tf"
	missing map[string]bool
}

var csvFileSuffix = map[Timeframe]string{
	Timeframe1m:  "1m",
	Timeframe5m:  "5m",
	Timeframe15m: "15m",
	Timeframe1h:  "1h",
	Timeframe4h:  "4h",
	Timeframe1d:  "1d",
}

// NewCSVProvider returns a provider reading CSVs from dir. tailKeep overrides
// the default retained window per timeframe.
func NewCSVProvider(dir string, tailKeep map[Timeframe]int) *CSVProvider {
	if tailKeep == nil {
		tailKeep = map[Timeframe]int{
			Timeframe1m:  4000,
			Timeframe5m:  4000,
			Timeframe15m: 4000,
			Timeframe1h:  4000,
			Timeframe4h:  4000,
			Timeframe1d:  50000,
		}
	}
	return &CSVProvider{
		dir:      dir,
		tailKeep: tailKeep,
		loaded:   map[string][]Candle{},
		missing:  map[string]bool{},
	}
}

// filePrefix maps an asset to its CSV file prefix, e.g. XAUUSD → "XAU".
func filePrefix(a Asset) string {
	sym := strings.ToUpper(a.Symbol)
	if a.QuoteCurrency != "" {
		if p := strings.TrimSuffix(sym, strings.ToUpper(a.QuoteCurrency)); p != sym {
			return p
		}
	}
	return sym
}

// GetCandles returns the candles in [from, to] for asset/timeframe, ascending.
// Only the retained tail is searchable — earlier data returns whatever subset
// overlaps the window.
func (p *CSVProvider) GetCandles(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) ([]Candle, error) {
	candles, err := p.load(asset, tf)
	if err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(candles))
	for _, c := range candles {
		if !c.Time.Before(from) && !c.Time.After(to) {
			out = append(out, c)
		}
	}
	return out, nil
}

// Tail returns the last n retained candles ending at or before asOf.
func (p *CSVProvider) Tail(asset Asset, tf Timeframe, n int, asOf time.Time) ([]Candle, error) {
	candles, err := p.load(asset, tf)
	if err != nil {
		return nil, err
	}
	end := len(candles)
	for end > 0 && candles[end-1].Time.After(asOf) {
		end--
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	return candles[start:end], nil
}

func (p *CSVProvider) load(asset Asset, tf Timeframe) ([]Candle, error) {
	suffix, ok := csvFileSuffix[tf]
	if !ok {
		return nil, fmt.Errorf("csv provider: unsupported timeframe %q", tf)
	}
	key := filePrefix(asset) + "|" + suffix

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.missing[key] {
		return nil, fmt.Errorf("csv provider: no data for %s %s", asset.Symbol, tf)
	}
	if candles, ok := p.loaded[key]; ok {
		return candles, nil
	}

	path := filepath.Join(p.dir, filePrefix(asset)+"_"+suffix+"_data.csv")
	f, err := os.Open(path)
	if err != nil {
		p.missing[key] = true
		return nil, fmt.Errorf("csv provider: open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	keep := p.tailKeep[tf]
	if keep <= 0 {
		keep = 4000
	}
	ring := make([]Candle, 0, keep)
	pos, total := 0, 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 256*1024)
	for sc.Scan() {
		c, err := parseCSVRow(sc.Text())
		if err != nil {
			continue // header or malformed line
		}
		if total < keep {
			ring = append(ring, c)
		} else {
			ring[pos] = c
		}
		pos = (pos + 1) % keep
		total++
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("csv provider: read %s: %w", filepath.Base(path), err)
	}
	if len(ring) == 0 {
		p.missing[key] = true
		return nil, fmt.Errorf("csv provider: %s has no rows", filepath.Base(path))
	}

	// Unwrap the ring into ascending order.
	candles := make([]Candle, 0, len(ring))
	if total <= keep {
		candles = ring
	} else {
		candles = append(candles, ring[pos:]...)
		candles = append(candles, ring[:pos]...)
	}
	p.loaded[key] = candles
	return candles, nil
}

// parseCSVRow parses "2004.06.11 00:00;384;384.8;382.8;384.1;272".
func parseCSVRow(line string) (Candle, error) {
	var c Candle
	f := strings.Split(line, ";")
	if len(f) < 6 {
		return c, fmt.Errorf("bad row: %q", line)
	}
	t, err := time.Parse("2006.01.02 15:04", f[0])
	if err != nil {
		return c, err
	}
	c.Time = t.UTC()
	nums := []*float64{&c.Open, &c.High, &c.Low, &c.Close, &c.Volume}
	for i, dst := range nums {
		v, err := strconv.ParseFloat(strings.TrimSpace(f[i+1]), 64)
		if err != nil {
			return c, err
		}
		*dst = v
	}
	return c, nil
}
