package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// UserStore è ciò che il service richiede alla persistenza degli utenti.
// L'interfaccia è dichiarata qui, dal chiamante; internal/postgres la implementa.
//
// Go: postgres.UserRepo non nomina mai UserStore: lo soddisfa perché ha i
// metodi giusti (interfacce implicite). Così auth non importa postgres, e nei
// test si passa un fake in memoria (fakes_test.go) senza database.
type UserStore interface {
	// CreateUser restituisce ErrEmailTaken se l'email esiste già
	// (confronto case-insensitive).
	CreateUser(ctx context.Context, u NewUser) (User, error)
	// CredentialsByEmail restituisce ErrNotFound se l'utente non esiste.
	CredentialsByEmail(ctx context.Context, email string) (Credentials, error)
}

// SessionStore è ciò che il service richiede alla persistenza delle sessioni.
type SessionStore interface {
	CreateSession(ctx context.Context, s Session) error
	// PrincipalBySession restituisce ErrNotFound se la sessione non esiste
	// o se expires_at <= now.
	PrincipalBySession(ctx context.Context, tokenHash []byte, now time.Time) (Principal, error)
	// DeleteSession è idempotente: nessun errore se la sessione non esiste.
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

const maxEmailLen = 254

// Service implementa i casi d'uso di autenticazione.
//
// Go: i campi sono minuscoli: fuori dal package nessuno può sostituire uno
// store o l'orologio dopo la creazione. L'unico modo di costruire un Service
// valido è NewService.
type Service struct {
	users    UserStore
	sessions SessionStore
	ttl      time.Duration
	now      func() time.Time // l'orologio: time.Now in produzione, fisso nei test
}

// NewService crea il service. now è iniettabile per testare la scadenza
// delle sessioni; se nil si usa time.Now.
//
// Go: time.Now senza parentesi è la funzione stessa, non l'ora attuale: la
// salviamo e la chiameremo con s.now() ogni volta che serve l'ora.
func NewService(users UserStore, sessions SessionStore, ttl time.Duration, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{users: users, sessions: sessions, ttl: ttl, now: now}
}

// Register crea sempre un requester: il ruolo non è scelto dal client.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	// Go: qui "email, err :=" riusa il parametro email (lo riassegna) e
	// dichiara err: := va bene se almeno una variabile a sinistra è nuova.
	email, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("register: %w", err)
	}
	u, err := s.users.CreateUser(ctx, NewUser{Email: email, PasswordHash: hash, Role: RoleRequester})
	if err != nil {
		// %w: ErrEmailTaken resta riconoscibile con errors.Is anche dopo
		// il prefisso "register: ".
		return User{}, fmt.Errorf("register: %w", err)
	}
	return u, nil
}

// Login verifica le credenziali e crea una sessione. Qualsiasi problema con
// email o password produce lo stesso ErrInvalidCredentials, così il client
// non può distinguere "utente inesistente" da "password errata".
func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	email, err := NormalizeEmail(email)
	if err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	creds, err := s.users.CredentialsByEmail(ctx, email)
	// Go: lo switch senza espressione separa i tre esiti: utente assente,
	// errore tecnico (DB giù: va a 500, non a 401), e successo, che esce
	// dallo switch senza entrare in nessun case.
	switch {
	case errors.Is(err, ErrNotFound):
		// Stesso costo di bcrypt anche quando l'utente non esiste.
		// Go: "_ =" scarta il risultato in modo esplicito: qui conta solo
		// il tempo speso, non l'esito.
		_ = CheckPassword(dummyHash, password)
		return LoginResult{}, ErrInvalidCredentials
	case err != nil:
		return LoginResult{}, fmt.Errorf("login: %w", err)
	}

	if err := CheckPassword(creds.PasswordHash, password); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("login: %w", err)
	}

	// Go: tre risultati in un colpo solo; il token in chiaro esiste solo in
	// questa variabile e nella risposta al client.
	token, hash, err := NewToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("login: %w", err)
	}
	// Go: time.Time contiene anche il fuso. UTC() lo normalizza, così le
	// date salvate e restituite non dipendono dal fuso della macchina.
	// Add somma una time.Duration e restituisce un nuovo time.Time.
	now := s.now().UTC()
	sess := Session{TokenHash: hash, UserID: creds.ID, CreatedAt: now, ExpiresAt: now.Add(s.ttl)}
	if err := s.sessions.CreateSession(ctx, sess); err != nil {
		return LoginResult{}, fmt.Errorf("login: %w", err)
	}
	return LoginResult{Token: token, ExpiresAt: sess.ExpiresAt}, nil
}

// Authenticate risolve un bearer token nel principal corrispondente.
// Token vuoto, sconosciuto, scaduto o revocato producono ErrUnauthenticated.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthenticated // inutile interrogare il DB
	}
	// Al DB arriva solo l'hash: è l'unica cosa che vi è salvata.
	p, err := s.sessions.PrincipalBySession(ctx, HashToken(token), s.now().UTC())
	switch {
	case errors.Is(err, ErrNotFound):
		return Principal{}, ErrUnauthenticated
	case err != nil:
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	return p, nil
}

// Logout revoca la sessione associata al token. È idempotente.
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.sessions.DeleteSession(ctx, HashToken(token)); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// NormalizeEmail rifiuta indirizzi non validi o con display name
// ("Ada <ada@example.com>") e restituisce l'email in minuscolo.
//
// Go: funzione con la maiuscola, quindi esportata: oltre a Register e Login
// la usa cmd/seed, così l'agent della demo segue le stesse regole.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLen {
		return "", fmt.Errorf("%w: email is required (max %d characters)", ErrInvalidInput, maxEmailLen)
	}
	// Go: net/mail è il parser RFC 5322 della libreria standard. Accetta
	// anche "Ada <ada@example.com>": per questo confrontiamo addr.Address
	// con l'input e rifiutiamo se differiscono.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", fmt.Errorf("%w: email is not valid", ErrInvalidInput)
	}
	return email, nil
}
