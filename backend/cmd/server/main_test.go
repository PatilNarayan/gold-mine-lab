//go:build integration

// Graceful-shutdown test now runs under the `integration` tag because run()
// applies migrations and bootstraps — it needs a real DATABASE_URL and
// MARKET_DATA_DIR (see .env.example / Makefile).
package main

import (
	"fmt"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestGracefulShutdown starts the real server, confirms it serves, injects
// SIGTERM, and asserts run() drains and returns without error.
func TestGracefulShutdown(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("HTTP_PORT", "18099")
	t.Setenv("LOG_LEVEL", "error")
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL unset — skipping integration test")
	}
	if os.Getenv("MARKET_DATA_DIR") == "" {
		t.Skip("MARKET_DATA_DIR unset — skipping integration test")
	}
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")

	shutdown := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() { done <- run(shutdown) }()

	// Wait until the listener is up.
	var up bool
	for i := 0; i < 50; i++ {
		res, err := http.Get("http://127.0.0.1:18099/api/health")
		if err == nil {
			res.Body.Close()
			up = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !up {
		t.Fatal("server did not start listening")
	}

	shutdown <- syscall.SIGTERM

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() returned error after SIGTERM: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return within shutdown timeout")
	}

	// The port must be released.
	_, err := http.Get("http://127.0.0.1:18099/api/health")
	if err == nil {
		t.Fatal("server still accepting connections after shutdown")
	}
	fmt.Println("graceful shutdown verified")
}
