package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embedded tz DB so Europe/Kyiv loads on every OS
)

var errPartial = errors.New("partial failure")

// brokerLoc is the timestamp convention of the existing dataset: MT5 broker
// time, EET/EEST (verified empirically — UTC+3 in summer, UTC+2 in winter —
// against provider closes at multiple windows). Provider data is converted
// into this convention before appending so the spliced series stays honest.
var brokerLoc = func() *time.Location {
	l, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		panic(err)
	}
	return l
}()

// tfSpec maps a dataset file to the provider interval used to extend it and
// the broker-side bar convention. 15m/1h buckets coincide across whole-hour
// offsets so fetched bars only need relabeling; 4h and daily broker bars do
// not align to UTC buckets and are aggregated from the 1h fetch instead.
type tfSpec struct {
	TF          string // dataset timeframe id ("15m")
	File        string // CSV file name
	Fetch       string // Twelve Data interval requested
	Bar         time.Duration
	AggregateTo kyivBucket // "" = relabel fetched bars, "4h"/"day" = re-bucket
}

type kyivBucket string

var tfSpecs = map[string]tfSpec{
	"15m": {TF: "15m", File: "XAU_15m_data.csv", Fetch: "15min", Bar: 15 * time.Minute},
	"1h":  {TF: "1h", File: "XAU_1h_data.csv", Fetch: "1h", Bar: time.Hour},
	"4h":  {TF: "4h", File: "XAU_4h_data.csv", Fetch: "1h", Bar: 4 * time.Hour, AggregateTo: "4h"},
	"1d":  {TF: "1d", File: "XAU_1d_data.csv", Fetch: "1h", Bar: 24 * time.Hour, AggregateTo: "day"},
}

const (
	maxBarsPerRequest = 4000 // provider outputsize cap is 5000; margin for safety
	volumeWhenAbsent  = 0.0  // XAU/USD supplies no volume — written as 0, documented in manifest
)

// SyncConfig carries the operator's selections.
type SyncConfig struct {
	SrcDir, OutDir   string
	Timeframes       []string
	Start, End       *time.Time // UTC bounds override (broker anchor used when nil)
	MaxRequests      int
	Tolerance        float64
	DryRun           bool
	RESTBase, APIKey string
	RequestGap       time.Duration // pacing between upstream calls
	Now              time.Time
}

type csvRow struct {
	T             time.Time // broker wall time (parsed naive, treated as Kyiv)
	O, H, L, C, V float64
}

// fetchCandle is one provider row, UTC.
type fetchCandle struct {
	T          time.Time
	O, H, L, C float64
	V          *float64
}

// tfReport is the per-timeframe acceptance record.
type tfReport struct {
	Timeframe      string   `json:"timeframe"`
	File           string   `json:"file"`
	SourceLastBar  *string  `json:"sourceLastBar"`
	AppendedFrom   *string  `json:"appendedFrom"`
	FirstAppended  *string  `json:"firstAppended"`
	LastAppended   *string  `json:"lastAppended"`
	SourceRows     int      `json:"sourceRows"`
	AppendedRows   int      `json:"appendedRows"`
	ResultRows     int      `json:"resultRows"`
	RejectedRows   int      `json:"rejectedRows"`
	Dupes          int      `json:"duplicates"`
	InvalidOHLC    int      `json:"invalidOHLC"`
	Discontinuity  string   `json:"discontinuityNote,omitempty"`
	OverlapMatched int      `json:"overlapMatchedRows"`
	OverlapMaxDiff float64  `json:"overlapMaxRelDiff"`
	OverlapNote    string   `json:"overlapNote,omitempty"`
	Gaps           []string `json:"gapNotes,omitempty"`
	SourceSHA256   string   `json:"sourceSha256"`
	ResultSHA256   string   `json:"resultSha256"`
	Requests       int      `json:"requests"`
	Error          string   `json:"error,omitempty"`
}

// manifest documents exactly what the snapshot is and where it came from.
type manifest struct {
	Version        int         `json:"manifestVersion"`
	GeneratedAt    string      `json:"generatedAtUtc"`
	Asset          string      `json:"asset"`
	Provider       string      `json:"provider"`
	SourceSymbol   string      `json:"sourceSymbol"`
	SourceDataset  string      `json:"sourceDataset"`
	SourceDir      string      `json:"-"`
	TimeConvention string      `json:"timeConvention"`
	Normalization  string      `json:"normalization"`
	VolumePolicy   string      `json:"volumePolicy"`
	IntervalMap    interface{} `json:"providerIntervals"`
	Requests       int         `json:"apiRequests"`
	EstCredits     int         `json:"estimatedCredits"`
	DryRun         bool        `json:"dryRun"`
	Files          []tfReport  `json:"files"`
	Notes          []string    `json:"notes,omitempty"`
}

// Report is the human/CI summary returned by Sync.
type Report struct {
	Man      *manifest
	Requests int
	Failed   []string
	Text     func() string
}

// Sync executes or previews the incremental dataset refresh.
func Sync(cfg SyncConfig) (*Report, error) {
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, err
	}
	man := &manifest{
		Version:        1,
		GeneratedAt:    cfg.Now.UTC().Format(time.RFC3339),
		Asset:          "xauusd",
		Provider:       "twelvedata",
		SourceSymbol:   "XAU/USD",
		SourceDataset:  "data-xauusd-v1 (MT5 export)",
		SourceDir:      cfg.SrcDir,
		TimeConvention: "CSV rows are MT5 broker wall time (EET/EEST ≈ Europe/Kyiv); provider data fetched timezone=UTC and converted",
		Normalization:  "15m/1h bars relabeled UTC→Kyiv; 4h/1d aggregated from provider 1h bars into Kyiv buckets",
		VolumePolicy:   "XAU/USD supplies no volume; appended rows carry 0",
		IntervalMap:    tfSpecs,
		DryRun:         cfg.DryRun,
	}

	// Copy every source file verbatim into the snapshot first — originals
	// are immutable; files outside the synced set stay byte-identical.
	if !cfg.DryRun {
		if err := mirrorFiles(cfg.SrcDir, cfg.OutDir); err != nil {
			return nil, err
		}
	}

	reqUsed := 0
	var failed []string
	specs := orderedSpecs(cfg.Timeframes)

	// cachedFetch dedupes identical windows across timeframes — the 1h
	// series serves 1h/4h/1d — paces requests, enforces the cap, and
	// retries a 429 once after a longer wait. Every request costs 1 credit.
	fetchCache := map[string][]fetchCandle{}
	lastReq := time.Time{}
	cachedFetch := func(interval string, start, end time.Time) ([]fetchCandle, error) {
		k := interval + "|" + start.Format(time.RFC3339) + "|" + end.Format(time.RFC3339)
		if b, ok := fetchCache[k]; ok {
			return b, nil
		}
		if reqUsed >= cfg.MaxRequests {
			return nil, fmt.Errorf("request cap %d reached", cfg.MaxRequests)
		}
		bars, err := fetchWindowPaced(cfg, interval, start, end, &reqUsed, &lastReq)
		if err == nil {
			fetchCache[k] = bars
		}
		return bars, err
	}

	for _, spec := range specs {
		rep := &tfReport{Timeframe: spec.TF, File: spec.File}
		man.Files = append(man.Files, *rep) // placeholder replaced below
		// Work on the OUT copy for idempotent re-runs: anchor = last accepted
		// row in the snapshot file (or the source file on first run).
		baseDir := cfg.OutDir
		base := filepath.Join(baseDir, spec.File)
		if _, err := os.Stat(base); err != nil {
			base = filepath.Join(cfg.SrcDir, spec.File)
		}
		if cfg.DryRun {
			base = filepath.Join(cfg.SrcDir, spec.File)
		}

		rows, last, contigEnd, discontinuity, err := scanCSV(base)
		if err != nil {
			rep.Error = fmt.Sprintf("read source: %v", err)
			failed = append(failed, spec.TF)
			man.Files[len(man.Files)-1] = *rep
			continue
		}
		rep.SourceRows = len(rows)
		rep.Discontinuity = discontinuity
		lt := last.T.Format("2006.01.02 15:04")
		rep.SourceLastBar = &lt
		if sum, err := fileSHA256(base); err == nil {
			rep.SourceSHA256 = "sha256:" + sum
		}

		// Window: broker anchor → convert to UTC for provider bounds.
		startUTC := brokerToUTC(last.T).Add(spec.Bar)
		if cfg.Start != nil {
			startUTC = cfg.Start.UTC()
		}
		endUTC := cfg.Now.UTC().Truncate(spec.fetchBar())
		if cfg.End != nil {
			endUTC = cfg.End.UTC()
		}
		if !endUTC.After(startUTC) {
			// Nothing to append — still record the snapshot file's identity so
			// the manifest always proves what the snapshot contains.
			rep.OverlapNote = "no new bars to fetch"
			rep.ResultRows = rep.SourceRows
			rep.ResultSHA256 = rep.SourceSHA256
			man.Files[len(man.Files)-1] = *rep
			continue
		}

		// Overlap validation: provider window over the source tail confirms
		// the timestamp convention actually reconciles before we splice.
		windows := planWindows(startUTC, endUTC, spec.fetchBar())
		rep.Requests = len(windows) + 1 // estimate incl. overlap probe
		if cfg.DryRun {
			rep.OverlapNote = "dry run — no requests made"
			man.Files[len(man.Files)-1] = *rep
			continue
		}

		// Probe the contiguous bulk tail (not literal last rows — stray
		// spliced-on rows sit past a discontinuity and would fail overlap).
		probeStart := brokerToUTC(contigEnd.T).Add(-30 * spec.fetchBar())
		probe, err := cachedFetch(spec.Fetch, probeStart, brokerToUTC(contigEnd.T).Add(spec.Bar))
		if err != nil {
			rep.Error = fmt.Sprintf("overlap fetch: %v", err)
			failed = append(failed, spec.TF)
			man.Files[len(man.Files)-1] = *rep
			continue
		}
		matched, maxDiff, note := overlapCheck(rows, probe, spec, cfg.Tolerance)
		rep.OverlapMatched, rep.OverlapMaxDiff, rep.OverlapNote = matched, maxDiff, note
		if matched < 3 {
			rep.Error = fmt.Sprintf("overlap validation failed: only %d source rows reconcile with the provider within %.2f%% — refusing to splice incompatible conventions", matched, cfg.Tolerance*100)
			failed = append(failed, spec.TF)
			man.Files[len(man.Files)-1] = *rep
			continue
		}

		var appended []csvRow
		for _, w := range windows {
			bars, ferr := cachedFetch(spec.Fetch, w.start, w.end)
			if ferr != nil {
				rep.Error = fmt.Sprintf("fetch %s–%s: %v", w.start.Format("2006-01-02"), w.end.Format("2006-01-02"), ferr)
				failed = append(failed, spec.TF)
				break
			}
			appended = append(appended, toBroker(bars, spec)...)
			if rep.FirstAppended == nil && len(appended) > 0 {
				s := appended[0].T.Format("2006-01-02 15:04")
				rep.FirstAppended = &s
			}
		}
		if rep.Error != "" {
			man.Files[len(man.Files)-1] = *rep
			continue
		}

		// Validation: strict monotone broker timestamps after the anchor,
		// plausible OHLC, no duplicates, no overlap with existing rows.
		valid, stats := validateAppended(last, appended, spec.Bar)
		rep.RejectedRows = len(appended) - len(valid)
		rep.Dupes = stats.dupes
		rep.InvalidOHLC = stats.invalidOHLC
		rep.Gaps = stats.gapNotes
		if len(valid) == 0 && len(appended) == 0 {
			rep.OverlapNote += "; provider returned no new bars"
		}
		if len(valid) > 0 {
			first := valid[0].T.Format("2006-01-02 15:04")
			lastA := valid[len(valid)-1].T.Format("2006-01-02 15:04")
			rep.FirstAppended = &first
			rep.LastAppended = &lastA
		}

		if err := appendRows(base, valid); err != nil {
			rep.Error = fmt.Sprintf("write: %v", err)
			failed = append(failed, spec.TF)
			man.Files[len(man.Files)-1] = *rep
			continue
		}
		rep.AppendedRows = len(valid)
		rep.ResultRows = rep.SourceRows + len(valid)
		if sum, err := fileSHA256(base); err == nil {
			rep.ResultSHA256 = "sha256:" + sum
		}
		man.Files[len(man.Files)-1] = *rep
	}
	man.Requests = reqUsed
	man.EstCredits = reqUsed // every call here is a single-credit /time_series

	if !cfg.DryRun {
		mb, _ := json.MarshalIndent(man, "", "  ")
		if err := os.WriteFile(filepath.Join(cfg.OutDir, "manifest.json"), mb, 0o644); err != nil {
			return nil, err
		}
	}

	rep := &Report{Man: man, Requests: reqUsed, Failed: failed}
	rep.Text = func() string { return renderReport(man) }
	var err error
	if len(failed) > 0 {
		err = errPartial
	}
	return rep, err
}

type window struct{ start, end time.Time }

func (s tfSpec) fetchBar() time.Duration {
	switch s.Fetch {
	case "15min":
		return 15 * time.Minute
	default:
		return time.Hour
	}
}

// planWindows splits [start,end] into ≤4000-bar chunks on an absolute grid
// (multiples of 4000 bars since the epoch), so every timeframe requesting
// the same provider interval produces IDENTICAL windows and shares the
// fetch cache. Bounds are inclusive on both ends for the provider API.
func planWindows(start, end time.Time, bar time.Duration) []window {
	grid := maxBarsPerRequest * bar
	var out []window
	cur := start.Truncate(grid)
	for cur.Before(end) || cur.Equal(end) {
		e := cur.Add(grid).Add(-bar)
		if e.After(end) {
			e = end
		}
		out = append(out, window{cur, e})
		cur = e.Add(bar)
	}
	return out
}

// scanCSV reads the MT5-format file and reports the last row, the last row
// of the contiguous bulk tail (before any spliced-on strays), and a note
// when the tail is discontinuous (the existing files jump Jul→Sep).
func scanCSV(path string) (rows []csvRow, last, contigEnd csvRow, note string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, last, contigEnd, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, last, contigEnd, "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || (i == 0 && strings.HasPrefix(strings.ToLower(ln), "date")) {
			continue
		}
		r, perr := parseCSVLine(ln)
		if perr != nil {
			continue // malformed rows are skipped, not fatal — counted upstream
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, last, contigEnd, "", fmt.Errorf("no candle rows in %s", filepath.Base(path))
	}
	// Detect a discontinuity inside the last 200 rows: a forward jump of
	// more than 30 days means the tail is not a contiguous series.
	last = rows[len(rows)-1]
	contigEnd = last
	prev := last.T
	for i := len(rows) - 2; i >= 0 && i >= len(rows)-200; i-- {
		if d := prev.Sub(rows[i].T); d > 30*24*time.Hour {
			contigEnd = rows[i]
			note = fmt.Sprintf("tail discontinuity: contiguous series ends %s; %d later row(s) up to %s appear spliced on",
				rows[i].T.Format("2006-01-02 15:04"), len(rows)-1-i, last.T.Format("2006-01-02 15:04"))
			break
		}
		prev = rows[i].T
	}
	return rows, last, contigEnd, note, nil
}

func parseCSVLine(ln string) (csvRow, error) {
	var r csvRow
	f := strings.Split(ln, ";")
	if len(f) < 6 {
		return r, fmt.Errorf("bad row")
	}
	// Broker wall time — no zone attached on disk; we treat it as Kyiv.
	t, err := time.ParseInLocation("2006.01.02 15:04", strings.TrimSpace(f[0]), brokerLoc)
	if err != nil {
		return r, err
	}
	r.T = t
	nums := []*float64{&r.O, &r.H, &r.L, &r.C, &r.V}
	for i, d := range nums {
		v, err := strconv.ParseFloat(strings.TrimSpace(f[i+1]), 64)
		if err != nil {
			return r, err
		}
		*d = v
	}
	return r, nil
}

// brokerToUTC interprets a CSV/broker timestamp as Kyiv wall time → UTC.
func brokerToUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, brokerLoc).UTC()
}

// utcToBroker converts a provider UTC instant to the broker wall clock.
func utcToBroker(t time.Time) time.Time {
	return t.In(brokerLoc)
}

// toBroker maps fetched UTC bars onto the file's broker convention:
// relabel for 15m/1h, re-bucket into Kyiv 4h/day bars for the coarser files.
func toBroker(bars []fetchCandle, spec tfSpec) []csvRow {
	if spec.AggregateTo == "" {
		out := make([]csvRow, 0, len(bars))
		for _, b := range bars {
			v := volumeWhenAbsent
			if b.V != nil {
				v = *b.V
			}
			out = append(out, csvRow{T: utcToBroker(b.T), O: b.O, H: b.H, L: b.L, C: b.C, V: v})
		}
		return out
	}
	buckets := map[time.Time]*csvRow{}
	var order []time.Time
	for _, b := range bars {
		kb := utcToBroker(b.T)
		var key time.Time
		if spec.AggregateTo == "day" {
			key = time.Date(kb.Year(), kb.Month(), kb.Day(), 0, 0, 0, 0, brokerLoc)
		} else { // 4h
			key = time.Date(kb.Year(), kb.Month(), kb.Day(), kb.Hour()/4*4, 0, 0, 0, brokerLoc)
		}
		c, ok := buckets[key]
		if !ok {
			c = &csvRow{T: key, O: b.O, H: b.H, L: b.L, C: b.C}
			buckets[key] = c
			order = append(order, key)
		} else {
			if b.H > c.H {
				c.H = b.H
			}
			if b.L < c.L {
				c.L = b.L
			}
			c.C = b.C
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Before(order[j]) })
	out := make([]csvRow, 0, len(order))
	for _, k := range order {
		out = append(out, *buckets[k])
	}
	return out
}

// overlapCheck aligns the source tail against the provider window (both in
// broker time) and counts rows whose closes reconcile within tolerance.
func overlapCheck(src []csvRow, prov []fetchCandle, spec tfSpec, tol float64) (int, float64, string) {
	pb := toBroker(prov, spec)
	byTime := map[time.Time]float64{}
	for _, r := range pb {
		byTime[r.T] = r.C
	}
	matched, checked := 0, 0
	maxDiff := 0.0
	for _, r := range src[maxInt(0, len(src)-40):] {
		p, ok := byTime[r.T]
		if !ok {
			continue
		}
		checked++
		d := math.Abs(p-r.C) / math.Max(math.Abs(r.C), 1)
		if d <= tol {
			matched++
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	return matched, maxDiff, fmt.Sprintf("%d/%d overlapping rows within %.2f%%", matched, checked, tol*100)
}

type valStats struct {
	dupes, invalidOHLC int
	gapNotes           []string
}

// validateAppended keeps strictly-ordered plausible rows after the anchor.
// Gaps are reported, never filled — market closures are legitimate.
func validateAppended(anchor csvRow, rows []csvRow, bar time.Duration) ([]csvRow, valStats) {
	var st valStats
	var out []csvRow
	prev := anchor.T
	for _, r := range rows {
		switch {
		case !r.T.After(prev):
			st.dupes++
			continue
		case !plausible(r):
			st.invalidOHLC++
			continue
		}
		if gap := r.T.Sub(prev); gap > 4*bar && gap < 30*24*time.Hour {
			st.gapNotes = append(st.gapNotes, fmt.Sprintf("%s gap %s→%s (possible closure — verify)", bar, prev.Format("2006-01-02 15:04"), r.T.Format("2006-01-02 15:04")))
		}
		out = append(out, r)
		prev = r.T
	}
	return out, st
}

func plausible(r csvRow) bool {
	for _, v := range []float64{r.O, r.H, r.L, r.C} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return false
		}
	}
	return r.H >= math.Max(r.O, r.C) && r.L <= math.Min(r.O, r.C) && r.H >= r.L
}

func appendRows(path string, rows []csvRow) error {
	if len(rows) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, r := range rows {
		if _, err := fmt.Fprintf(f, "%s;%.5f;%.5f;%.5f;%.5f;%.0f\n",
			r.T.Format("2006.01.02 15:04"), r.O, r.H, r.L, r.C, r.V); err != nil {
			return err
		}
	}
	return nil
}

// fetchWindowPaced calls /time_series for one bounded window, UTC on both
// sides, honoring the pacing gap between requests and retrying a 429 once
// after a longer wait. Every upstream attempt counts toward the cap.
func fetchWindowPaced(cfg SyncConfig, interval string, start, end time.Time, used *int, lastReq *time.Time) ([]fetchCandle, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if gap := cfg.RequestGap - time.Since(*lastReq); gap > 0 && !lastReq.IsZero() {
			time.Sleep(gap)
		}
		*lastReq = time.Now()
		*used++
		bars, err := fetchWindow(cfg, interval, start, end)
		if err == nil {
			return bars, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "429") {
			break // non-rate-limit failure — no retry
		}
		time.Sleep(maxDur(60*time.Second, 4*cfg.RequestGap))
	}
	return nil, lastErr
}

// fetchWindow issues the raw /time_series call.
func fetchWindow(cfg SyncConfig, interval string, start, end time.Time) ([]fetchCandle, error) {
	u, err := url.Parse(cfg.RESTBase + "/time_series")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("symbol", "XAU/USD")
	q.Set("interval", interval)
	q.Set("outputsize", strconv.Itoa(maxBarsPerRequest))
	q.Set("order", "ASC")
	q.Set("timezone", "UTC")
	q.Set("start_date", start.Format("2006-01-02 15:04:05"))
	q.Set("end_date", end.Format("2006-01-02 15:04:05"))
	q.Set("apikey", cfg.APIKey)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("provider request failed: %w", ue.Err) // never the URL (it carries the key)
		}
		return nil, errors.New("provider request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("provider rate limit (429) — retry later")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider HTTP %d", resp.StatusCode)
	}
	var ts struct {
		Values []struct {
			Datetime               string `json:"datetime"`
			Open, High, Low, Close string
			Volume                 *string `json:"volume"`
		} `json:"values"`
		Status  string `json:"status"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &ts); err != nil {
		return nil, fmt.Errorf("malformed provider response: %w", err)
	}
	if ts.Status == "error" || ts.Code != 0 {
		msg := ts.Message
		if msg == "" {
			msg = "provider error"
		}
		if ts.Code == 401 || ts.Code == 403 {
			return nil, fmt.Errorf("credentials/plan rejected: %s", msg)
		}
		if ts.Code == 429 {
			return nil, errors.New("provider rate limit (429) — retry later")
		}
		return nil, fmt.Errorf("provider error %d: %s", ts.Code, msg)
	}
	out := make([]fetchCandle, 0, len(ts.Values))
	for _, v := range ts.Values {
		c, err := parseFetch(v.Datetime, v.Open, v.High, v.Low, v.Close, v.Volume)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func parseFetch(dt, o, h, l, c string, vol *string) (fetchCandle, error) {
	var fc fetchCandle
	var err error
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		fc.T, err = time.ParseInLocation(layout, dt, time.UTC)
		if err == nil {
			break
		}
	}
	if err != nil {
		return fc, fmt.Errorf("bad provider datetime %q", dt)
	}
	fc.T = fc.T.UTC()
	for _, d := range []struct {
		s string
		p *float64
	}{{o, &fc.O}, {h, &fc.H}, {l, &fc.L}, {c, &fc.C}} {
		v, err := strconv.ParseFloat(d.s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return fc, fmt.Errorf("bad provider price %q", d.s)
		}
		*d.p = v
	}
	if vol != nil && *vol != "" {
		if v, err := strconv.ParseFloat(*vol, 64); err == nil {
			fc.V = &v
		}
	}
	return fc, nil
}

// mirrorFiles copies every .csv in src verbatim into out (snapshot base).
func mirrorFiles(src, out string) error {
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".csv") {
			continue
		}
		dst := filepath.Join(out, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue // re-run: keep the snapshot's own version
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func orderedSpecs(tfs []string) []tfSpec {
	order := []string{"1d", "4h", "1h", "15m"}
	want := map[string]bool{}
	for _, t := range tfs {
		want[strings.TrimSpace(t)] = true
	}
	var out []tfSpec
	for _, o := range order {
		if want[o] {
			out = append(out, tfSpecs[o])
		}
	}
	return out
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sameDir(a, b string) bool {
	aa, e1 := filepath.Abs(a)
	bb, e2 := filepath.Abs(b)
	return e1 == nil && e2 == nil && aa == bb
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func renderReport(m *manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== XAUUSD history sync (%s → %s) ===\n", m.SourceSymbol, m.SourceDataset)
	fmt.Fprintf(&b, "provider=%s requests=%d estCredits=%d dryRun=%v\n\n", m.Provider, m.Requests, m.EstCredits, m.DryRun)
	for _, f := range m.Files {
		fmt.Fprintf(&b, "[%s] %s\n", f.Timeframe, f.File)
		if f.Error != "" {
			fmt.Fprintf(&b, "  ERROR: %s\n", f.Error)
		}
		if f.SourceLastBar != nil {
			fmt.Fprintf(&b, "  source: %d rows through %s\n", f.SourceRows, *f.SourceLastBar)
		}
		if f.Discontinuity != "" {
			fmt.Fprintf(&b, "  %s\n", f.Discontinuity)
		}
		fmt.Fprintf(&b, "  overlap: %s\n", f.OverlapNote)
		if f.AppendedRows > 0 {
			fmt.Fprintf(&b, "  appended %d rows (%s → %s); rejected=%d dupes=%d badOHLC=%d\n",
				f.AppendedRows, strOr(f.FirstAppended), strOr(f.LastAppended), f.RejectedRows, f.Dupes, f.InvalidOHLC)
		}
		for _, g := range f.Gaps {
			fmt.Fprintf(&b, "  gap: %s\n", g)
		}
		if f.ResultSHA256 != "" {
			fmt.Fprintf(&b, "  result sha256 %s\n", f.ResultSHA256)
		}
	}
	return b.String()
}

func strOr(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}
