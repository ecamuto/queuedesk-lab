package auth

import (
	"errors"
	"fmt"
)

// TokenBytes è l'entropia del token di sessione: 32 byte = 256 bit.
const TokenBytes = 32

// NewToken genera un token opaco casuale e il suo hash.
// Il token (base64url senza padding, 43 caratteri) va al client;
// l'hash va al database.
//
// Go: "(token string, hash []byte, err error)" sono risultati con nome:
// nascono al valore zero e documentano cosa restituisce la funzione. Qui li
// usiamo solo come documentazione: ogni return elenca i valori in modo
// esplicito, che resta più leggibile di un return "nudo".
func NewToken() (token string, hash []byte, err error) {
	// TODO(AUTH-1, passo B) — Generare il token consegnato al client.
	//
	// Dove serve: Login (AUTH-2) chiama NewToken dopo aver verificato la
	// password. Il token torna al client nel body JSON; nella tabella
	// sessions finisce soltanto l'hash. Completa prima HashToken (passo A).
	//
	// Perché così: il token è la credenziale di sessione. Deve essere
	// impossibile da indovinare e viaggiare in header e URL senza escaping.
	//
	// Implementazione:
	//   1. aggiungi gli import "crypto/rand" e "encoding/base64";
	//   2. b := make([]byte, TokenBytes): 32 byte = 256 bit;
	//   3. riempi b con rand.Read(b) di crypto/rand, mai math/rand (è
	//      prevedibile). Read restituisce (n, err): scarta n con _;
	//   4. se err != nil restituisci "", nil e
	//      fmt.Errorf("generate token: %w", err). Da Go 1.24 Read non
	//      fallisce, ma il controllo costa nulla e documenta l'intento;
	//   5. token = base64.RawURLEncoding.EncodeToString(b): alfabeto URL-safe
	//      (A-Z a-z 0-9 - _) senza padding "="; 32 byte diventano 43
	//      caratteri. Usa "=" e non ":=": token è già dichiarato nella firma;
	//   6. return token, HashToken(token), nil. L'hash si calcola sulla
	//      stringa token, non sui byte b: Authenticate riceverà la stringa.
	//
	// Rimuovi lo stub e l'import "errors", che non servirà più ("fmt" resta).
	// Non loggare token né byte casuali.
	// Fatto quando: make test-acceptance RUN='^TestNewToken' (formato, 32
	// byte di entropia, hash coerente, nessun duplicato su 100 generazioni).
	return "", nil, fmt.Errorf("TODO(AUTH-1) NewToken: %w", errors.ErrUnsupported)
}

// HashToken restituisce SHA-256(token). Un hash veloce va bene qui (a
// differenza delle password) perché il token ha 256 bit di entropia: non è
// attaccabile a dizionario.
func HashToken(token string) []byte {
	// TODO(AUTH-1, passo A) — Calcolare l'identificatore salvato nel database.
	//
	// Dove serve: NewToken (passo B) per l'hash da salvare; Authenticate e
	// Logout (AUTH-3) per ritrovare la sessione a partire dal token ricevuto.
	// Per lo stesso token devono ottenere sempre gli stessi 32 byte.
	//
	//   1. aggiungi l'import "crypto/sha256";
	//   2. sum := sha256.Sum256([]byte(token));
	//   3. Sum256 restituisce [32]byte, un array; la firma vuole []byte, una
	//      slice: restituisci sum[:], che vede lo stesso array senza copiarlo.
	//
	// SHA-256 e non bcrypt: qui non si protegge una password scelta da una
	// persona, si indicizza un valore con 256 bit casuali, che non ha
	// dizionario. Un hash veloce è corretto, e ogni richiesta autenticata lo
	// ricalcola. Sostituisci "return nil".
	// Fatto quando: il package compila; il test arriva con il passo B
	// (TestNewToken confronta HashToken(token) con SHA-256(token)).
	return nil
}
