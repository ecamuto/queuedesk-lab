// Command seed crea l'utente agent per la demo. È l'unico modo previsto per
// ottenere un agent: /v1/auth/register crea sempre requester.
//
//	SEED_AGENT_PASSWORD=... go run ./cmd/seed -email agent@queuedesk.local
//
// La password arriva da una variabile d'ambiente, non da un flag, per non
// lasciarla nella history della shell o nell'elenco dei processi.
// Se l'email esiste già il comando non modifica nulla: in particolare non
// promuove ad agent un requester esistente.
//
// Go: un secondo "package main" in un'altra cartella di cmd/ produce un
// secondo eseguibile. Condivide i package di internal/ con cmd/api.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"queuedesk/internal/auth"
	"queuedesk/internal/postgres"
)

// Stessa struttura di cmd/api e cmd/migrate: main resta minima, la logica
// sta in run, che restituisce un error.
func main() {
	if err := run(); err != nil {
		// Go: gli errori vanno su stderr (os.Stderr), l'output normale su
		// stdout: chi usa il comando in uno script li può separare.
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	// Go: flag.String registra il flag -email e restituisce un *string. Il
	// valore viene scritto solo da flag.Parse, quindi si legge dopo, con *email.
	email := flag.String("email", "agent@queuedesk.local", "email of the demo agent")
	flag.Parse()

	// Stessa normalizzazione di Register: senza, un " Agent@X.local " con
	// gli spazi verrebbe salvato così e il login non lo troverebbe mai.
	addr, err := auth.NormalizeEmail(*email)
	if err != nil {
		return err
	}

	// Stesse regole di validazione della registrazione: le costanti
	// esportate da auth evitano di duplicare i numeri.
	password := os.Getenv("SEED_AGENT_PASSWORD")
	if len(password) < auth.MinPasswordLen || len(password) > auth.MaxPasswordLen {
		return fmt.Errorf("SEED_AGENT_PASSWORD must be between %d and %d bytes", auth.MinPasswordLen, auth.MaxPasswordLen)
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is not set")
	}

	// Un comando da riga di comando non deve restare appeso per sempre se
	// il database non risponde: 30 secondi per tutto.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	// Si usa direttamente il repository, non auth.Service: Register crea
	// sempre un requester, e questo è l'unico punto che può creare un agent.
	u, err := postgres.NewUserRepo(pool).CreateUser(ctx, auth.NewUser{Email: addr, PasswordHash: hash, Role: auth.RoleAgent})
	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		fmt.Printf("user %s already exists: nothing changed\n", addr)
		return nil // rieseguire il seed non è un errore (idempotente)
	case err != nil:
		return err
	}
	fmt.Printf("agent created: id=%s email=%s\n", u.ID, u.Email)
	return nil
}
