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
	// TODO(AUTH-2) — Verificare le credenziali e creare una sessione.
	//
	// Dove serve: l'handler POST /v1/auth/login (auth_handlers.go) passa qui
	// email e password. respondErr (errors.go) traduce ErrInvalidCredentials
	// in 401 invalid_credentials e ogni errore non riconosciuto in 500.
	//
	// Regola: ogni fallimento imputabile al client restituisce ESATTAMENTE
	// ErrInvalidCredentials, non avvolto (il test confronta con ==): il client
	// non deve capire se l'email esiste. I guasti di store, hashing o
	// generazione del token restano errori interni: fmt.Errorf("login: %w", err).
	//
	// Passo A — Normalizzazione e ricerca utente:
	//   1. email, err := NormalizeEmail(email) (riassegna il parametro);
	//   2. errore → return LoginResult{}, ErrInvalidCredentials. Non
	//      ErrInvalidInput: diventerebbe 400 e rivelerebbe il motivo;
	//   3. creds, err := s.users.CredentialsByEmail(ctx, email);
	//   4. errors.Is(err, ErrNotFound) → esegui comunque
	//      _ = CheckPassword(dummyHash, password), poi restituisci
	//      ErrInvalidCredentials. Senza il confronto fittizio un'email
	//      inesistente risponderebbe molto prima (timing side channel);
	//   5. ogni altro errore dello store → fmt.Errorf("login: %w", err).
	//
	// Passo B — Verifica password:
	//   1. CheckPassword(creds.PasswordHash, password): nil se corrisponde;
	//   2. errors.Is(err, ErrInvalidCredentials) → restituisci
	//      ErrInvalidCredentials, il valore originale e non err avvolto;
	//   3. ogni altro errore (hash corrotto nel DB) → "login: %w".
	//
	// Passo C — Creazione sessione, solo dopo una password corretta:
	//   1. token, hash, err := NewToken(); errore → "login: %w";
	//   2. now := s.now().UTC(), non time.Now(): i test iniettano un orologio
	//      fisso e confrontano gli istanti esatti;
	//   3. costruisci Session con TokenHash hash, UserID creds.ID, CreatedAt
	//      now ed ExpiresAt now.Add(s.ttl), poi s.sessions.CreateSession;
	//   4. errore di salvataggio → "login: %w";
	//   5. return LoginResult{Token: token, ExpiresAt: <ExpiresAt della
	//      sessione>}, nil.
	//
	// Il token in chiaro esce soltanto nel risultato: non salvarlo, non
	// loggarlo. Rimuovi lo stub; "errors" resta negli import per errors.Is.
	// Fatto quando: make test-acceptance RUN='^TestLogin'.
	// TestLoginStoreFailureIsNotACredentialError è verde già con lo stub:
	// deve restarlo anche dopo.
	return LoginResult{}, fmt.Errorf("TODO(AUTH-2) Login: %w", errors.ErrUnsupported)
}

// Authenticate risolve un bearer token nel principal corrispondente.
// Token vuoto, sconosciuto, scaduto o revocato producono ErrUnauthenticated.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	// TODO(AUTH-3, passo A) — Risolvere il token in un'identità.
	//
	// Dove serve: requireAuth (AUTH-4) chiama Authenticate a ogni richiesta
	// protetta, con il token letto dall'header Authorization.
	//
	//   1. token == "" → return Principal{}, ErrUnauthenticated, senza
	//      interrogare lo store;
	//   2. lo store conosce solo l'hash: cerca con HashToken(token);
	//   3. p, err := s.sessions.PrincipalBySession(ctx, hash, s.now().UTC()).
	//      La scadenza la controlla lo store (valida solo se expires_at > now):
	//      qui non confrontare date;
	//   4. errors.Is(err, ErrNotFound) significa token sconosciuto, scaduto o
	//      revocato, casi che il client non deve distinguere: restituisci
	//      ErrUnauthenticated, che HTTP mapperà a 401;
	//   5. ogni altro errore è tecnico: fmt.Errorf("authenticate: %w", err),
	//      che diventerà 500;
	//   6. altrimenti return p, nil.
	//
	// Non trasformare un DB irraggiungibile in 401: il client crederebbe
	// scaduta una sessione valida e disconnetterebbe l'utente.
	// Prerequisiti: AUTH-1 e AUTH-2, perché il test fa prima un Login.
	// Fatto quando: make test-acceptance RUN='^TestAuthenticate'.
	// TestAuthenticateStoreFailureIsInternal è verde già con lo stub: deve
	// restarlo anche dopo.
	return Principal{}, fmt.Errorf("TODO(AUTH-3) Authenticate: %w", errors.ErrUnsupported)
}

// Logout revoca la sessione associata al token. È idempotente.
func (s *Service) Logout(ctx context.Context, token string) error {
	// TODO(AUTH-3, passo B) — Revocare la sessione.
	//
	// Dove serve: DELETE /v1/auth/session, dietro requireAuth: quando si
	// arriva qui il token è già stato verificato.
	//
	//   1. s.sessions.DeleteSession(ctx, HashToken(token)): passa l'hash, mai
	//      il token in chiaro;
	//   2. errore → fmt.Errorf("logout: %w", err); altrimenti return nil.
	//
	// Non controllare se la sessione esisteva: DeleteSession è idempotente, e
	// un secondo logout (doppio clic, retry di rete) deve riuscire.
	// Rimuovi lo stub.
	// Fatto quando: make test-acceptance RUN=TestLogoutRevokesSession; poi
	// RUN=TestAuthServiceWithPostgres prova AUTH-1..3 su PostgreSQL vero.
	return fmt.Errorf("TODO(AUTH-3) Logout: %w", errors.ErrUnsupported)
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
