package auth

import (
	"errors"
	"fmt"

	// Go: un modulo esterno si importa con il suo percorso completo; la
	// versione esatta è fissata in go.mod (e verificata da go.sum).
	// golang.org/x/... sono librerie mantenute dal team Go ma fuori dalla
	// libreria standard.
	"golang.org/x/crypto/bcrypt"
)

// Limiti sulla password. bcrypt considera al massimo 72 byte: oltre, le
// password diverse ma con lo stesso prefisso sarebbero equivalenti.
//
// Go: costanti con la maiuscola, quindi esportate: cmd/seed le riusa per
// validare la password della demo con le stesse regole.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 72
)

// dummyHash è l'hash bcrypt (cost 10) di una stringa casuale. Login lo
// confronta quando l'email non esiste, così il tempo di risposta non rivela
// se un account è registrato.
const dummyHash = "$2a$10$3Fo5/AqrAINZrhr6b3JcnOyfQ37F5t4lsZvFOLgzYf24oynJu/vOm"

// HashPassword calcola l'hash bcrypt della password.
//
// bcrypt è lento apposta (DefaultCost = 10: decine di millisecondi per hash) e include un
// salt casuale: due hash della stessa password sono diversi.
//
// Go: []byte(password) converte la string in una slice di byte (copiandola);
// string(h) fa il contrario. bcrypt lavora su []byte, noi su string.
func HashPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// CheckPassword restituisce nil se la password corrisponde all'hash,
// ErrInvalidCredentials se non corrisponde, un altro errore se l'hash è
// corrotto.
//
// Go: qui si traducono gli errori di una libreria in errori del nostro
// dominio. Chi chiama CheckPassword non deve sapere che sotto c'è bcrypt.
func CheckPassword(hash, password string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	// Go: in un case si possono elencare più condizioni separate da virgola:
	// basta che una sia vera.
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword), errors.Is(err, bcrypt.ErrPasswordTooLong):
		return ErrInvalidCredentials
	default:
		return fmt.Errorf("check password: %w", err)
	}
}

// validatePassword controlla la lunghezza in byte, come la conta bcrypt.
//
// Go: len su una string restituisce i byte, non i caratteri: "è" ne occupa
// 2 in UTF-8. Per contare i caratteri serve utf8.RuneCountInString (lo usa
// il package ticket per il titolo).
func validatePassword(password string) error {
	if len(password) < MinPasswordLen || len(password) > MaxPasswordLen {
		return fmt.Errorf("%w: password must be between %d and %d bytes",
			ErrInvalidInput, MinPasswordLen, MaxPasswordLen)
	}
	return nil
}
