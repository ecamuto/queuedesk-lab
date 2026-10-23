// Package auth gestisce registrazione, login e sessioni con token opaco.
//
// Il token viene mostrato al client una sola volta, nella risposta di login.
// Nel database si conserva soltanto SHA-256(token): chi legge un dump non può
// usarlo per impersonare gli utenti.
//
// Go: il package è diviso in file per tema (auth.go i tipi, password.go,
// token.go, service.go la logica). Questo file contiene solo tipi ed errori:
// nessuna dipendenza da HTTP o SQL, quindi si importa da tutti senza cicli.
package auth

import (
	"errors"
	"time"
)

// Role è il ruolo di un utente. La registrazione crea sempre RoleRequester;
// RoleAgent si ottiene solo tramite il seed esplicito (cmd/seed).
//
// Go: "type Role string" crea un tipo nuovo con una string sotto. Il
// compilatore non accetta una string qualsiasi dove serve un Role senza
// conversione esplicita, Role("..."): i ruoli validi restano le costanti qui
// sotto. Le costanti tipizzate (Role = "...") hanno già il tipo Role.
type Role string

const (
	RoleRequester Role = "requester"
	RoleAgent     Role = "agent"
)

// User è un utente senza dati sensibili: si può restituire al client.
type User struct {
	ID        string // UUID generato da PostgreSQL
	Email     string
	Role      Role
	CreatedAt time.Time
}

// Credentials aggiunge l'hash della password. Non esce mai dal package auth
// verso HTTP.
//
// Go: "User" senza nome di campo è un campo incorporato (embedding): i campi
// di User si leggono direttamente, c.Email invece di c.User.Email. Non è
// ereditarietà: una Credentials non si può passare dove serve una User, va
// scritto c.User esplicitamente.
type Credentials struct {
	User
	PasswordHash string
}

// NewUser sono i dati necessari a creare un utente.
type NewUser struct {
	Email        string
	PasswordHash string
	Role         Role
}

// Session è ciò che viene salvato nel database: hash del token, mai il token.
//
// Go: []byte è una slice di byte, il tipo naturale per dati binari come un
// hash (in SQL diventa BYTEA). Una string Go può contenere byte qualsiasi, ma
// []byte rende chiaro che non è testo.
type Session struct {
	TokenHash []byte
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Principal è l'identità autenticata associata a una richiesta.
type Principal struct {
	UserID string
	Email  string
	Role   Role
}

// IsAgent riporta se il principal ha il ruolo agent.
//
// Go: receiver per valore "(p Principal)": il metodo lavora su una copia,
// che va bene perché non modifica nulla e la struct è piccola.
func (p Principal) IsAgent() bool { return p.Role == RoleAgent }

// LoginResult contiene il token in chiaro: va restituito al client e poi
// dimenticato.
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
}

// Errori restituiti dal service e dagli store. Il layer HTTP li traduce in
// status code con errors.Is.
//
// Go: sono errori "sentinella": valori creati una volta sola con errors.New
// e confrontati per identità. Il service li avvolge con %w per aggiungere
// contesto ("register: email already registered") ed errors.Is li ritrova.
var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUnauthenticated    = errors.New("unauthenticated")
	// ErrNotFound è restituito dagli store quando la riga non esiste
	// (o la sessione è scaduta). Il service lo traduce: non arriva a HTTP.
	ErrNotFound = errors.New("not found")
)
