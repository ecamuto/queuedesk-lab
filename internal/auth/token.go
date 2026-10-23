package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
	// Go: crypto/rand legge dal generatore casuale del sistema operativo,
	// adatto a segreti. math/rand è prevedibile: mai per token o password.
	b := make([]byte, TokenBytes)
	// Go: ":=" dentro l'if crea un nuovo err, visibile solo nell'if, che
	// "copre" (shadowing) il risultato con nome err. Va bene perché nel
	// return lo passiamo esplicitamente.
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("generate token: %w", err)
	}
	// RawURLEncoding: alfabeto sicuro negli URL e negli header (- e _ al
	// posto di + e /) e niente "=" finale.
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashToken(token), nil
}

// HashToken restituisce SHA-256(token). Un hash veloce va bene qui (a
// differenza delle password) perché il token ha 256 bit di entropia: non è
// attaccabile a dizionario.
func HashToken(token string) []byte {
	// Go: Sum256 restituisce un array [32]byte, di lunghezza fissa nel tipo.
	// sum[:] ne fa una slice che punta allo stesso array, senza copiarlo.
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
