# QueueDesk: comandi di sviluppo. `make` senza argomenti mostra l'elenco.

SHELL := /bin/bash
.DEFAULT_GOAL := help

# .env è opzionale; si esportano solo le variabili dell'applicazione, così
# TEST_DATABASE_URL non attiva per sbaglio gli integration test in `make test`.
-include .env
export DATABASE_URL HTTP_ADDR SESSION_TTL SHUTDOWN_TIMEOUT NOTIFY_WORKERS NOTIFY_QUEUE_SIZE

SEED_AGENT_EMAIL    ?= agent@queuedesk.local
SEED_AGENT_PASSWORD ?= agent-demo-password
TEST_DATABASE_URL   ?= postgres://queuedesk:queuedesk@localhost:55432/queuedesk_test?sslmode=disable
BASE_URL            ?= http://localhost:8080

.PHONY: help db-up db-down db-reset db-psql migrate-up migrate-down migrate-redo seed run build \
        fmt vet test test-race test-integration test-all cover smoke check test-acceptance todo

help: ## Mostra i target disponibili
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}'

## --- database ---
db-up: ## Avvia PostgreSQL (compose) e attende l'healthcheck
	docker compose up -d --wait db

db-down: ## Ferma PostgreSQL mantenendo i dati
	docker compose down

db-reset: ## Distrugge il volume e ricrea il database (PERDE I DATI)
	docker compose down -v
	docker compose up -d --wait db

db-psql: ## Apre psql nel container
	docker compose exec db psql -U queuedesk -d queuedesk

migrate-up: ## Applica le migrazioni mancanti
	go run ./cmd/migrate up

migrate-down: ## Annulla l'ultima migrazione
	go run ./cmd/migrate down

migrate-redo: ## Riapplica l'ultima migrazione su DB di sviluppo e di test
	go run ./cmd/migrate down
	go run ./cmd/migrate up
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate down
	DATABASE_URL="$(TEST_DATABASE_URL)" go run ./cmd/migrate up

seed: ## Crea l'agent demo (SEED_AGENT_EMAIL / SEED_AGENT_PASSWORD)
	SEED_AGENT_PASSWORD="$(SEED_AGENT_PASSWORD)" go run ./cmd/seed -email "$(SEED_AGENT_EMAIL)"

## --- applicazione ---
run: ## Avvia l'API (Ctrl+C per lo shutdown ordinato)
	go run ./cmd/api

build: ## Compila i binari in bin/
	go build -o bin/ ./cmd/...

## --- qualità ---
fmt: ## Formatta il codice
	go fmt ./...

vet: ## Analisi statica
	go vet ./...

test: ## Unit test (integration saltati senza TEST_DATABASE_URL)
	go test ./...

test-race: ## Unit test con race detector
	go test -race ./...

test-integration: ## Integration test PostgreSQL (richiede make db-up)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./internal/postgres/...

test-all: ## Tutti i test, integration inclusi, con race detector
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -race -count=1 ./...

cover: ## Copertura dei test (apre il report HTML)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -count=1 -coverprofile=cover.out ./...
	go tool cover -html=cover.out

check: fmt vet test-race ## fmt + vet + test-race

## --- laboratorio ---
RUN ?= .
test-acceptance: ## Acceptance test dei TODO; filtra con RUN=TestNome (richiede make db-up)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test -tags acceptance -race -count=1 -timeout 2m -run '$(RUN)' ./...

todo: ## Elenca i TODO del laboratorio ancora presenti
	@grep -rn --include='*.go' --include='*.sql' 'TODO(' . | sed 's|^\./||'

## --- demo ---
smoke: ## Demo end-to-end con curl (richiede make run e make seed)
	BASE_URL="$(BASE_URL)" SEED_AGENT_EMAIL="$(SEED_AGENT_EMAIL)" SEED_AGENT_PASSWORD="$(SEED_AGENT_PASSWORD)" ./scripts/smoke.sh
