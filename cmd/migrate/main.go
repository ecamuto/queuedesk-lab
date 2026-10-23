// Command migrate applica o annulla le migrazioni SQL in migrations/.
//
//	go run ./cmd/migrate up          applica tutte le migrazioni mancanti
//	go run ./cmd/migrate down        annulla l'ultima migrazione
//	go run ./cmd/migrate down -steps 2
//
// Legge DATABASE_URL dall'ambiente.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"queuedesk/internal/postgres"
	"queuedesk/migrations"
)

// Stessa struttura di cmd/api: main resta minima, la logica sta in run, che
// restituisce un error.
func main() {
	// Go: os.Args contiene il nome del programma seguito dagli argomenti;
	// os.Args[1:] è la slice dal secondo elemento in poi.
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || (args[0] != "up" && args[0] != "down") {
		return errors.New("usage: migrate up | migrate down [-steps N]")
	}
	cmd := args[0]
	fs := flag.NewFlagSet("migrate "+cmd, flag.ContinueOnError)
	// Go: fs.Int restituisce un *int, un puntatore: il valore viene scritto
	// da fs.Parse e si legge con *steps.
	steps := fs.Int("steps", 1, "number of migrations to roll back (down only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is not set (did you copy .env.example to .env?)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	pool, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var done []string
	switch cmd {
	case "up":
		done, err = postgres.MigrateUp(ctx, pool, migrations.FS)
	case "down":
		if *steps < 1 {
			return errors.New("-steps must be >= 1")
		}
		done, err = postgres.MigrateDown(ctx, pool, migrations.FS, *steps)
	}
	if err != nil {
		return err
	}
	if len(done) == 0 {
		fmt.Println("nothing to do")
	}
	for _, v := range done {
		fmt.Printf("%s %s\n", cmd, v)
	}
	return nil
}
