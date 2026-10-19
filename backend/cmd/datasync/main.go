// datasync produces a versioned XAUUSD historical snapshot by appending
// verified Twelve Data candles to a copy of the existing CSV dataset.
// The source dataset is never modified; the snapshot is selected at run
// time via MARKET_DATA_DIR like any other dataset directory.
//
// Usage:
//
//	go run ./cmd/datasync -src ../../data -out ../../data-twelvedata-v2 [-dry-run]
//
// TWELVE_DATA_API_KEY must be set in the environment (never a flag — it
// must not appear in argv, logs, or process lists).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	var (
		src      = flag.String("src", "", "source dataset directory (original CSVs, never modified)")
		out      = flag.String("out", "", "output snapshot directory (created; re-run appends to it)")
		start    = flag.String("start", "", "override window start (UTC date YYYY-MM-DD); default = last row + 1 bar")
		end      = flag.String("end", "", "override window end (UTC date YYYY-MM-DD); default = latest completed bar")
		tfs      = flag.String("timeframes", "1d,4h,1h,15m", "comma-separated timeframes to sync")
		maxReq   = flag.Int("max-requests", 40, "hard cap on upstream /time_series requests")
		reqGap   = flag.Duration("request-interval", 8*time.Second, "minimum delay between upstream requests (rate-limit pacing)")
		tol      = flag.Float64("overlap-tolerance", 0.0015, "max relative close deviation for overlap match")
		dryRun   = flag.Bool("dry-run", false, "print the fetch/merge plan without any upstream requests or writes")
		restBase = flag.String("rest-url", "https://api.twelvedata.com", "Twelve Data REST base (override for tests)")
	)
	flag.Parse()

	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "both -src and -out are required")
		os.Exit(2)
	}
	key := os.Getenv("TWELVE_DATA_API_KEY")
	if key == "" && !*dryRun {
		fmt.Fprintln(os.Stderr, "TWELVE_DATA_API_KEY is not set — configure it in the environment (never as a flag)")
		os.Exit(2)
	}
	if sameDir(*src, *out) {
		fmt.Fprintln(os.Stderr, "-out must differ from -src: the original dataset is never modified in place")
		os.Exit(2)
	}

	cfg := SyncConfig{
		SrcDir:      *src,
		OutDir:      *out,
		Timeframes:  splitCSV(*tfs),
		MaxRequests: *maxReq,
		Tolerance:   *tol,
		DryRun:      *dryRun,
		RESTBase:    *restBase,
		APIKey:      key,
		RequestGap:  *reqGap,
		Now:         time.Now().UTC(),
	}
	if *start != "" {
		t, err := time.Parse("2006-01-02", *start)
		if err != nil {
			fmt.Fprintln(os.Stderr, "-start must be YYYY-MM-DD")
			os.Exit(2)
		}
		cfg.Start = &t
	}
	if *end != "" {
		t, err := time.Parse("2006-01-02", *end)
		if err != nil {
			fmt.Fprintln(os.Stderr, "-end must be YYYY-MM-DD")
			os.Exit(2)
		}
		end := t.Add(24 * time.Hour) // end-of-day inclusive
		cfg.End = &end
	}

	rep, err := Sync(cfg)
	if err != nil {
		if !errors.Is(err, errPartial) {
			fmt.Fprintf(os.Stderr, "sync failed: %v\n", err)
			os.Exit(1)
		}
	}
	if rep != nil {
		fmt.Print(rep.Text())
	}
	if errors.Is(err, errPartial) {
		fmt.Fprintf(os.Stderr, "sync finished with per-timeframe failures (see report)\n")
		os.Exit(1)
	}
}
