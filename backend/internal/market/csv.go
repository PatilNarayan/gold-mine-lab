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

// Range returns every candle in [from, to] plus up to `warmup` candles
// immediately before `from` (for indicator warm-up — e.g. a 50-period EMA
// needs history before the window start). Unlike load(), nothing is retained:
// the file is streamed each call so arbitrary windows never exhaust memory.
// Rows failing validation abort the load with the offending line number.
func (p *CSVProvider) Range(asset Asset, tf Timeframe, from, to time.Time, warmup int) ([]Candle, error) {
	suffix, ok := csvFileSuffix[tf]
	if !ok {
		return nil, fmt.Errorf("csv provider: unsupported timeframe %q", tf)
	}
	path := filepath.Join(p.dir, filePrefix(asset)+"_"+suffix+"_data.csv")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("csv provider: open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	pre := make([]Candle, 0, warmup) // ring of the last `warmup` rows before `from`
	prePos := 0
	out := make([]Candle, 0, 1024)
	var prev time.Time

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 256*1024)
	line := 0
	for sc.Scan() {
		line++
		c, err := parseCSVRow(sc.Text())
		if err != nil {
			if line == 1 {
				continue // header row
			}
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
		if err := validateOHLC(c); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), line, err)
		}
		if prev.IsZero() {
			prev = c.Time
		} else {
			if c.Time.Equal(prev) {
				return nil, fmt.Errorf("%s:%d: duplicate timestamp %s", filepath.Base(path), line, c.Time)
			}
			if c.Time.Before(prev) {
				return nil, fmt.Errorf("%s:%d: unsorted timestamps (%s after %s)", filepath.Base(path), line, c.Time, prev)
			}
			prev = c.Time
		}
		if c.Time.After(to) {
			break // file is ascending — everything beyond `to` is irrelevant
		}
		if c.Time.Before(from) {
			if warmup > 0 {
				if len(pre) < warmup {
					pre = append(pre, c)
				} else {
					pre[prePos] = c
					prePos = (prePos + 1) % warmup
				}
			}
			continue
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("csv provider: read %s: %w", filepath.Base(path), err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("csv provider: no %s %s candles in [%s, %s]", asset.Symbol, tf, from, to)
	}

	// Prepend the warmup ring in ascending order.
	result := make([]Candle, 0, len(pre)+len(out))
	if len(pre) == warmup && prePos > 0 {
		result = append(result, pre[prePos:]...)
		result = append(result, pre[:prePos]...)
	} else {
		result = append(result, pre...)
	}
	return append(result, out...), nil
}

// validateOHLC enforces price sanity; a bad row rejects the load rather than
// silently corrupting results.
func validateOHLC(c Candle) error {
	if c.Time.IsZero() {
		return fmt.Errorf("zero timestamp")
	}
	if c.Open <= 0 || c.High <= 0 || c.Low <= 0 || c.Close <= 0 {
		return fmt.Errorf("non-positive OHLC value (o=%v h=%v l=%v c=%v)", c.Open, c.High, c.Low, c.Close)
	}
	if c.Volume < 0 {
		return fmt.Errorf("negative volume %v", c.Volume)
	}
	hi := c.Open
	if c.Close > hi {
		hi = c.Close
	}
	lo := c.Open
	if c.Close < lo {
		lo = c.Close
	}
	if c.High < c.Low || c.High < hi || c.Low > lo {
		return fmt.Errorf("invalid OHLC (o=%v h=%v l=%v c=%v)", c.Open, c.High, c.Low, c.Close)
	}
	return nil
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
