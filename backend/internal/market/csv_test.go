package market

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	content := "Date;Open;High;Low;Close;Volume\n" +
		"2025.10.10 09:00;4000.5;4001.2;3999.9;4000.8;100\n" +
		"2025.10.10 10:00;4000.8;4002.0;4000.1;4001.5;120\n" +
		"2025.10.13 09:00;4001.5;4003.0;4001.0;4002.7;90\n" +
		"2025.10.15 09:00;4002.7;4005.0;4002.0;4004.0;80\n"
	if err := os.WriteFile(filepath.Join(dir, "XAU_1h_data.csv"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

var xau = Asset{ID: "xauusd", Symbol: "XAUUSD", QuoteCurrency: "USD"}

func TestCSVProviderTail(t *testing.T) {
	p := NewCSVProvider(writeFixture(t), nil)
	asOf := time.Date(2025, 10, 14, 0, 0, 0, 0, time.UTC)

	got, err := p.Tail(xau, Timeframe1h, 10, asOf)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d candles, want 3 (row after asOf truncated)", len(got))
	}
	if got[2].Close != 4002.7 {
		t.Errorf("last close = %v, want 4002.7", got[2].Close)
	}
	if got[0].Time.Format("2006-01-02") != "2025-10-10" {
		t.Errorf("first time = %v", got[0].Time)
	}
}

func TestCSVProviderGetCandlesWindow(t *testing.T) {
	p := NewCSVProvider(writeFixture(t), nil)
	from := time.Date(2025, 10, 10, 10, 0, 0, 0, time.UTC)
	to := time.Date(2025, 10, 13, 9, 0, 0, 0, time.UTC)
	got, err := p.GetCandles(context.Background(), xau, Timeframe1h, from, to)
	if err != nil {
		t.Fatalf("GetCandles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candles, want 2", len(got))
	}
}

func TestCSVProviderMissingFile(t *testing.T) {
	p := NewCSVProvider(writeFixture(t), nil)
	msft := Asset{ID: "msft", Symbol: "MSFT", QuoteCurrency: "USD"}
	if _, err := p.Tail(msft, Timeframe1h, 10, time.Now()); err == nil {
		t.Fatal("expected error for missing file")
	}
}
