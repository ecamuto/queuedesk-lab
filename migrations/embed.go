// Package migrations incorpora i file SQL nel binario, così cmd/migrate e
// gli integration test usano esattamente gli stessi script.
package migrations

import "embed"

// FS contiene i file NNNN_nome.up.sql e NNNN_nome.down.sql.
// embed.FS è in sola lettura: non è stato globale mutabile.
//
// Go: "//go:embed" (senza spazio dopo //) non è un commento qualunque ma
// una direttiva per il compilatore: copia i file *.sql dentro il binario.
//
//go:embed *.sql
var FS embed.FS
