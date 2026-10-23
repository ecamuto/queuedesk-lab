package main

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Questi test avviano run come farebbe main, contro il database di test:
// senza TEST_DATABASE_URL vengono saltati.
//
// Go: "package main" (senza _test) perché run non è esportata: un test nello
// stesso package vede anche le funzioni minuscole.
func setupRunEnv(t *testing.T) {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	// Go: t.Setenv imposta la variabile solo per la durata del test e poi
	// ripristina il valore precedente.
	t.Setenv("DATABASE_URL", dbURL)
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")
}

// freeAddr chiede al sistema operativo una porta libera.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// startRun avvia run in una goroutine e restituisce un canale da cui
// arriverà il suo risultato.
//
// Go: "<-chan error" è un canale in sola ricezione: chi lo riceve può
// leggere ma non inviare. È lo stesso schema che userai in BASE-4.
func startRun(t *testing.T) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- run(slog.New(slog.DiscardHandler)) }()
	return done
}

func waitResult(t *testing.T, done <-chan error) error {
	t.Helper()
	// Go: select con time.After è il modo classico di mettere un timeout
	// all'attesa su un canale.
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return within 10s")
		return nil
	}
}

// waitHealthy attende che il server risponda; fallisce subito se run
// termina prima (per esempio con un errore TODO).
func waitHealthy(t *testing.T, url string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run returned before serving: %v", err)
		default:
		}
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s not healthy within 5s", url)
}

func TestRunGracefulShutdown(t *testing.T) {
	setupRunEnv(t)
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	done := startRun(t)
	waitHealthy(t, "http://"+addr+"/healthz", done)

	// Lo stesso segnale di Ctrl+C: signal.NotifyContext lo intercetta e il
	// processo di test non termina.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := waitResult(t, done); err != nil {
		t.Fatalf("run = %v, want nil after SIGINT", err)
	}
	if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
		t.Fatal("server still accepting connections after shutdown")
	}
}

func TestRunPortInUse(t *testing.T) {
	setupRunEnv(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("HTTP_ADDR", ln.Addr().String())

	err = waitResult(t, startRun(t))
	if err == nil || !strings.Contains(err.Error(), "http server") {
		t.Fatalf("run = %v, want an \"http server: ...\" error", err)
	}
}
