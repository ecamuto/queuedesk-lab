//go:build acceptance

// Acceptance test della tappa corrente: girano con `make test-acceptance`.
// Falliscono finché i TODO della tappa non sono completati (vedi TAPPA.md).

// Go: i file che finiscono in _test.go vengono compilati solo da `go test`.
// "package config_test" (con _test) è un package separato: il test vede solo
// ciò che config esporta, esattamente come un utente esterno.
package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"queuedesk/internal/config"
)

const validDB = "postgres://queuedesk:s3cret@localhost:5432/queuedesk?sslmode=disable"

// env trasforma una mappa in una funzione getenv.
//
// Go: env restituisce una funzione. Quella funzione è una closure: usa m
// anche dopo che env è terminata. Così config.Load legge dalla mappa invece
// che dall'ambiente del processo.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Go: un test è una funzione TestXxx(t *testing.T). t.Fatalf segnala il
// fallimento e ferma il test; t.Errorf lo segnala e prosegue.
func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": validDB}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Config{
		DatabaseURL:     validDB,
		HTTPAddr:        config.DefaultHTTPAddr,
		SessionTTL:      config.DefaultSessionTTL,
		ShutdownTimeout: config.DefaultShutdownTimeout,
		NotifyWorkers:   config.DefaultNotifyWorkers,
		NotifyQueueSize: config.DefaultNotifyQueueSize,
	}
	// Go: due struct si confrontano con == se tutti i campi sono confrontabili.
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadCustomValues(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{
		"DATABASE_URL":      validDB,
		"HTTP_ADDR":         "127.0.0.1:9090",
		"SESSION_TTL":       "2h",
		"SHUTDOWN_TIMEOUT":  "3s",
		"NOTIFY_WORKERS":    "8",
		"NOTIFY_QUEUE_SIZE": "500",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" || cfg.SessionTTL != 2*time.Hour ||
		cfg.ShutdownTimeout != 3*time.Second || cfg.NotifyWorkers != 8 || cfg.NotifyQueueSize != 500 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

// TestLoadErrors è un test "table-driven", lo stile tipico di Go: una
// tabella di casi (slice di struct anonime) e un ciclo che li esegue.
// Aggiungere un caso = aggiungere una riga.
func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr []string // sottostringhe attese nel messaggio
	}{
		{"missing database url", map[string]string{}, []string{"DATABASE_URL: required"}},
		{"wrong scheme", map[string]string{"DATABASE_URL": "mysql://x@localhost/db"}, []string{"scheme"}},
		{"missing host", map[string]string{"DATABASE_URL": "postgres:///db"}, []string{"host is missing"}},
		{"bad addr", map[string]string{"DATABASE_URL": validDB, "HTTP_ADDR": "8080"}, []string{"HTTP_ADDR"}},
		{"port not a number", map[string]string{"DATABASE_URL": validDB, "HTTP_ADDR": ":abc"}, []string{"HTTP_ADDR", "invalid port"}},
		{"port out of range", map[string]string{"DATABASE_URL": validDB, "HTTP_ADDR": ":99999"}, []string{"HTTP_ADDR", "invalid port"}},
		{"ttl without unit", map[string]string{"DATABASE_URL": validDB, "SESSION_TTL": "24"}, []string{"SESSION_TTL", "not a duration"}},
		{"ttl too short", map[string]string{"DATABASE_URL": validDB, "SESSION_TTL": "10s"}, []string{"SESSION_TTL", "out of range"}},
		{"shutdown too long", map[string]string{"DATABASE_URL": validDB, "SHUTDOWN_TIMEOUT": "1h"}, []string{"SHUTDOWN_TIMEOUT"}},
		{"zero workers", map[string]string{"DATABASE_URL": validDB, "NOTIFY_WORKERS": "0"}, []string{"NOTIFY_WORKERS", "out of range"}},
		{"workers not a number", map[string]string{"DATABASE_URL": validDB, "NOTIFY_WORKERS": "four"}, []string{"NOTIFY_WORKERS", "not an integer"}},
		{"queue too large", map[string]string{"DATABASE_URL": validDB, "NOTIFY_QUEUE_SIZE": "1000000"}, []string{"NOTIFY_QUEUE_SIZE"}},
		{
			"all errors reported together",
			map[string]string{"NOTIFY_WORKERS": "-1", "SESSION_TTL": "forever"},
			[]string{"DATABASE_URL", "NOTIFY_WORKERS", "SESSION_TTL"},
		},
	}
	// Go: range su una slice dà indice e valore; l'indice qui è scartato.
	// t.Run crea un sotto-test con nome: in caso di errore si vede quale
	// riga è fallita, e se ne può lanciare una sola con -run.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := config.Load(env(tt.env))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, s := range tt.wantErr {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not contain %q", err, s)
				}
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Errorf("error leaks the database password: %q", err)
			}
		})
	}
}

func TestLogValueRedactsPassword(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": validDB}))
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("start", "config", cfg)
	if strings.Contains(sb.String(), "s3cret") {
		t.Fatalf("log contains the password: %s", sb.String())
	}
	if !strings.Contains(sb.String(), "xxxxx") {
		t.Fatalf("log does not show the redacted URL: %s", sb.String())
	}
}

// TestLoadAcceptsServiceNamePort: una porta per nome (":http") è valida, quindi
// il controllo non può limitarsi a "la porta è un numero".
func TestLoadAcceptsServiceNamePort(t *testing.T) {
	cfg, err := config.Load(env(map[string]string{"DATABASE_URL": validDB, "HTTP_ADDR": "127.0.0.1:http"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:http" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
}
