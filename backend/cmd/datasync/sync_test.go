package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tdStub serves /time_series responses; rows is a full ascending series
// the stub slices to satisfy each start_date/end_date window.
func tdStub(t *testing.T, hits *atomic.Int32, rows []fetchCandle) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		q := r.URL.Query()
		start, _ := time.ParseInLocation("2006-01-02 15:04:05", q.Get("start_date"), time.UTC)
		end, _ := time.ParseInLocation("2006-01-02 15:04:05", q.Get("end_date"), time.UTC)
		var b strings.Builder
		b.WriteString(`{"values":[`)
		first := true
		for _, c := range rows {
			if c.T.Before(start) || c.T.After(end) {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, `{"datetime":"%s","open":"%v","high":"%v","low":"%v","close":"%v"}`,
				c.T.Format("2006-01-02 15:04:05"), c.O, c.H, c.L, c.C)
		}
		b.WriteString(`],"status":"ok"}`)
		_, _ = w.Write([]byte(b.String()))
	}))
}

func makeSeries(start time.Time, n int, step time.Duration, base float64) []fetchCandle {
	out := make([]fetchCandle, 0, n)
	for i := 0; i < n; i++ {
		p := base + float64(i)
		out = append(out, fetchCandle{T: start.Add(time.Duration(i) * step), O: p, H: p + 1, L: p - 1, C: p})
	}
	return out
}

// csvText renders fixture rows in the dataset's MT5 format.
func csvText(rows []csvRow) string {
	var b strings.Builder
	b.WriteString("Date;Open;High;Low;Close;Volume\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s;%.5f;%.5f;%.5f;%.5f;%.0f\n",
			r.T.Format("2006.01.02 15:04"), r.O, r.H, r.L, r.C, r.V)
	}
	return b.String()
}

func setupFixture(t *testing.T, tf string, src []csvRow) (srcDir, outDir string) {
	t.Helper()
	srcDir = t.TempDir()
	outDir = filepath.Join(t.TempDir(), "snap")
	name := tfSpecs[tf].File
	if err := os.WriteFile(filepath.Join(srcDir, name), []byte(csvText(src)), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir, outDir
}

func TestSyncAppendsAndIsIdempotent(t *testing.T) {
	// Source tail (broker/Kyiv wall time) matches provider UTC series at +3.
	now := time.Date(2025, 10, 2, 12, 0, 0, 0, time.UTC)
	kyiv := brokerLoc
	var src []csvRow
	baseUTC := time.Date(2025, 9, 30, 14, 0, 0, 0, time.UTC) // Kyiv 17:00 (+3)
	for i := 0; i < 10; i++ {
		u := baseUTC.Add(time.Duration(i) * 15 * time.Minute)
		p := 100.0 + float64(i)
		src = append(src, csvRow{T: u.In(kyiv), O: p, H: p + 1, L: p - 1, C: p, V: 7})
	}
	srcDir, outDir := setupFixture(t, "15m", src)

	// Provider series continues the same prices through the sync window.
	prov := makeSeries(baseUTC, 60, 15*time.Minute, 100)
	var hits atomic.Int32
	srv := tdStub(t, &hits, prov)
	defer srv.Close()

	cfg := SyncConfig{
		SrcDir: srcDir, OutDir: outDir, Timeframes: []string{"15m"},
		MaxRequests: 10, Tolerance: 0.01, RESTBase: srv.URL, APIKey: "k",
		RequestGap: 0, Now: now,
	}
	rep, err := Sync(cfg)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	f := rep.Man.Files[0]
	if f.AppendedRows == 0 || f.OverlapMatched < 3 {
		t.Fatalf("expected appended rows and overlap match: %+v", f)
	}
	got, _, _, _, err := scanCSV(filepath.Join(outDir, "XAU_15m_data.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(src)+f.AppendedRows {
		t.Fatalf("rows = %d, want %d+%d", len(got), len(src), f.AppendedRows)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].T.After(got[i-1].T) {
			t.Fatalf("not strictly ascending at %d", i)
		}
		if got[i].V != 0 && i >= len(src) {
			t.Fatalf("appended volume not 0 at %d", i)
		}
	}
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err != nil {
		t.Fatal("manifest.json missing")
	}

	// Idempotent: a second run fetches nothing and appends nothing.
	hits.Store(0)
	rep2, err := Sync(cfg)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if rep2.Man.Files[0].AppendedRows != 0 {
		t.Fatalf("second run appended %d rows", rep2.Man.Files[0].AppendedRows)
	}
	got2, _, _, _, _ := scanCSV(filepath.Join(outDir, "XAU_15m_data.csv"))
	if len(got2) != len(got) {
		t.Fatalf("second run changed row count %d→%d", len(got), len(got2))
	}
}

func TestSyncAbortsOnOverlapMismatch(t *testing.T) {
	var src []csvRow
	for i := 0; i < 10; i++ {
		u := time.Date(2025, 9, 30, 14, 0, 0, 0, time.UTC).Add(time.Duration(i) * 15 * time.Minute)
		src = append(src, csvRow{T: u.In(brokerLoc), O: 100, H: 101, L: 99, C: 100, V: 1})
	}
	srcDir, outDir := setupFixture(t, "15m", src)
	// Provider prices 50% off — incompatible feed/convention.
	prov := makeSeries(src[0].T.UTC(), 30, 15*time.Minute, 150)
	var hits atomic.Int32
	srv := tdStub(t, &hits, prov)
	defer srv.Close()

	rep, err := Sync(SyncConfig{
		SrcDir: srcDir, OutDir: outDir, Timeframes: []string{"15m"},
		MaxRequests: 10, Tolerance: 0.01, RESTBase: srv.URL, APIKey: "k", Now: time.Date(2025, 10, 2, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("expected overlap failure")
	}
	if rep.Man.Files[0].AppendedRows != 0 {
		t.Fatal("rows appended despite convention mismatch")
	}
}

func TestSyncHonorsRequestCap(t *testing.T) {
	var src []csvRow
	u := time.Date(2025, 9, 30, 0, 0, 0, 0, time.UTC)
	src = append(src, csvRow{T: u.In(brokerLoc), O: 1, H: 2, L: 0.5, C: 1.5, V: 1})
	srcDir, outDir := setupFixture(t, "15m", src)
	prov := makeSeries(u, 10, 15*time.Minute, 100)
	var hits atomic.Int32
	srv := tdStub(t, &hits, prov)
	defer srv.Close()
	rep, _ := Sync(SyncConfig{
		SrcDir: srcDir, OutDir: outDir, Timeframes: []string{"15m"},
		MaxRequests: 0, Tolerance: 0.01, RESTBase: srv.URL, APIKey: "k", Now: time.Date(2025, 10, 2, 0, 0, 0, 0, time.UTC),
	})
	if rep.Man.Files[0].Error == "" {
		t.Fatal("expected cap error")
	}
}

func TestToBrokerAggregatesKyivBuckets(t *testing.T) {
	// 1h UTC bars across an EEST day boundary → Kyiv-day aggregation.
	u := time.Date(2025, 7, 14, 20, 0, 0, 0, time.UTC) // 23:00 Kyiv (+3)
	bars := []fetchCandle{
		{T: u, O: 10, H: 12, L: 9, C: 11},
		{T: u.Add(time.Hour), O: 11, H: 15, L: 10, C: 14}, // Jul 15 00:00 Kyiv
		{T: u.Add(2 * time.Hour), O: 14, H: 16, L: 13, C: 15},
	}
	got := toBroker(bars, tfSpecs["1d"])
	if len(got) != 2 {
		t.Fatalf("want 2 kyiv-day buckets, got %d: %+v", len(got), got)
	}
	if got[0].T.Day() != 14 || got[1].T.Day() != 15 {
		t.Fatalf("bad buckets: %+v", got)
	}
	if got[1].O != 11 || got[1].H != 16 || got[1].L != 10 || got[1].C != 15 {
		t.Fatalf("bad aggregation: %+v", got[1])
	}

	got4 := toBroker(bars, tfSpecs["4h"])
	if len(got4) != 2 || got4[1].T.Hour() != 0 {
		t.Fatalf("4h bucketing wrong: %+v", got4)
	}
}

func TestValidateAppended(t *testing.T) {
	anchor := csvRow{T: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	rows := []csvRow{
		{T: anchor.T.Add(time.Hour), O: 1, H: 2, L: .5, C: 1.5},       // ok
		{T: anchor.T.Add(time.Hour), O: 1, H: 2, L: .5, C: 1.5},       // dupe
		{T: anchor.T.Add(2 * time.Hour), O: -1, H: 2, L: .5, C: 1.5},  // bad price
		{T: anchor.T.Add(2 * time.Hour), O: 1, H: 0.2, L: .5, C: 1.5}, // H<O
		{T: anchor.T.Add(3 * time.Hour), O: 1, H: 2, L: .5, C: 1.5},   // ok
	}
	valid, st := validateAppended(anchor, rows, time.Hour)
	if len(valid) != 2 || st.dupes != 1 || st.invalidOHLC != 2 {
		t.Fatalf("valid=%d dupes=%d invalid=%d", len(valid), st.dupes, st.invalidOHLC)
	}
}

func TestPlanWindowsGridAligned(t *testing.T) {
	bar := time.Hour
	w1 := planWindows(time.Date(2025, 9, 30, 21, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), bar)
	w2 := planWindows(time.Date(2025, 10, 1, 5, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), bar)
	if len(w1) != len(w2) {
		t.Fatalf("grid not shared: %d vs %d", len(w1), len(w2))
	}
	for i := range w1 {
		if !w1[i].start.Equal(w2[i].start) {
			t.Fatalf("windows differ: %v vs %v", w1[i].start, w2[i].start)
		}
	}
}
