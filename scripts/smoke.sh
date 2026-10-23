#!/usr/bin/env bash
# Demo end-to-end ripetibile di QueueDesk: solo bash e curl.
#
# Prerequisiti: API avviata (make run) e agent demo creato (make seed).
# Ogni esecuzione registra un requester nuovo, quindi lo script si può
# rilanciare quante volte si vuole.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
AGENT_EMAIL="${SEED_AGENT_EMAIL:-agent@queuedesk.local}"
AGENT_PASSWORD="${SEED_AGENT_PASSWORD:-agent-demo-password}"
RUN_ID="$(date +%s)-$$"
REQ_EMAIL="ada-${RUN_ID}@example.com"
OTHER_EMAIL="bob-${RUN_ID}@example.com"
PASSWORD="correct-horse-battery"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
pass=0
fail=0

# call METHOD PATH [BODY] [TOKEN]: stampa lo status, salva il body in $TMP/body.
call() {
  local method=$1 path=$2 body=${3:-} token=${4:-}
  local args=(-s -o "$TMP/body" -w '%{http_code}' -X "$method" "$BASE_URL$path")
  if [[ -n $body ]]; then args+=(-H 'Content-Type: application/json' -d "$body"); fi
  if [[ -n $token ]]; then args+=(-H "Authorization: Bearer $token"); fi
  curl "${args[@]}"
}

# expect DESCRIZIONE ATTESO OTTENUTO
expect() {
  if [[ $2 == "$3" ]]; then
    printf '  ok    %-52s %s\n' "$1" "$3"
    pass=$((pass + 1))
  else
    printf '  FAIL  %-52s got %s, want %s\n        body: %s\n' "$1" "$3" "$2" "$(cat "$TMP/body" 2>/dev/null)"
    fail=$((fail + 1))
  fi
}

# creds EMAIL PASSWORD [EXTRA]: body JSON per register/login.
# I body si costruiscono in variabili: con il bash 3.2 di macOS un JSON con
# virgole scritto dentro "$( ... )" subisce la brace expansion.
creds() {
  printf '{"email":"%s","password":"%s"%s}' "$1" "$2" "${3:-}"
}

# field NOME: estrae un campo stringa o numerico di primo livello dal body.
field() {
  sed -n "s/.*\"$1\":\"\{0,1\}\([^\",}]*\)\"\{0,1\}.*/\1/p" "$TMP/body" | head -n1
}

echo "== QueueDesk smoke test su $BASE_URL"
if ! curl -s -o /dev/null "$BASE_URL/healthz"; then
  echo "API non raggiungibile: avviala con 'make run' in un altro terminale." >&2
  exit 1
fi

echo "-- probe"
expect "GET /healthz" 200 "$(call GET /healthz)"
expect "GET /readyz" 200 "$(call GET /readyz)"

echo "-- registrazione e login"
body=$(creds "$REQ_EMAIL" "$PASSWORD")
expect "register requester" 201 "$(call POST /v1/auth/register "$body")"
body=$(creds "x-$RUN_ID@example.com" "$PASSWORD" ',"role":"agent"')
expect "register con ruolo agent -> unknown_field" 400 "$(call POST /v1/auth/register "$body")"
body=$(creds "ADA-$RUN_ID@example.com" "$PASSWORD")
expect "register duplicato (maiuscole) -> 409" 409 "$(call POST /v1/auth/register "$body")"
expect "body non JSON -> 415" 415 \
  "$(curl -s -o "$TMP/body" -w '%{http_code}' -X POST -d 'email=x' "$BASE_URL/v1/auth/login")"
body=$(creds "$REQ_EMAIL" wrong-password)
expect "login password errata -> 401" 401 "$(call POST /v1/auth/login "$body")"
body=$(creds "$REQ_EMAIL" "$PASSWORD")
expect "login requester" 200 "$(call POST /v1/auth/login "$body")"
REQ_TOKEN="$(field token)"
expect "GET /v1/me" 200 "$(call GET /v1/me "" "$REQ_TOKEN")"
expect "ruolo requester" requester "$(field role)"

echo "-- ticket"
body='{"title":"VPN down","description":"dalle 9:00"}'
expect "crea ticket" 201 "$(call POST /v1/tickets "$body" "$REQ_TOKEN")"
TICKET_ID="$(field id)"
body='{"title":"  "}'
expect "titolo vuoto -> 400" 400 "$(call POST /v1/tickets "$body" "$REQ_TOKEN")"
expect "lista del requester" 200 "$(call GET '/v1/tickets?status=open&limit=5' "" "$REQ_TOKEN")"
expect "limit non valido -> 400" 400 "$(call GET '/v1/tickets?limit=1000' "" "$REQ_TOKEN")"
expect "claim da requester -> 403" 403 "$(call POST "/v1/tickets/$TICKET_ID/claim" "" "$REQ_TOKEN")"

echo "-- agent"
body=$(creds "$AGENT_EMAIL" "$AGENT_PASSWORD")
status="$(call POST /v1/auth/login "$body")"
expect "login agent" 200 "$status"
if [[ $status != 200 ]]; then
  echo "Login agent fallito: hai eseguito 'make seed'?" >&2
  exit 1
fi
AGENT_TOKEN="$(field token)"
expect "coda dell'agent" 200 "$(call GET '/v1/tickets?status=open' "" "$AGENT_TOKEN")"

# Due claim simultanei: esattamente uno vince.
for i in 1 2; do
  curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $AGENT_TOKEN" \
    "$BASE_URL/v1/tickets/$TICKET_ID/claim" >"$TMP/claim$i" &
done
wait
codes="$(sort "$TMP/claim1" "$TMP/claim2" | tr '\n' ' ')"
echo '{}' >"$TMP/body"
expect "claim concorrenti: un 200 e un 409" "200 409 " "$codes"

resolved='{"status":"resolved"}'
in_progress='{"status":"in_progress"}'
expect "open -> resolved non ammesso (409)" 409 "$(call PATCH "/v1/tickets/$TICKET_ID/status" "$resolved" "$AGENT_TOKEN")"
expect "open -> in_progress" 200 "$(call PATCH "/v1/tickets/$TICKET_ID/status" "$in_progress" "$AGENT_TOKEN")"
expect "in_progress -> resolved" 200 "$(call PATCH "/v1/tickets/$TICKET_ID/status" "$resolved" "$AGENT_TOKEN")"

echo "-- visibilità e audit"
expect "owner legge il ticket" 200 "$(call GET "/v1/tickets/$TICKET_ID" "" "$REQ_TOKEN")"
expect "stato finale" resolved "$(field status)"
expect "audit del ticket" 200 "$(call GET "/v1/tickets/$TICKET_ID/events" "" "$REQ_TOKEN")"
expect "eventi: created, claimed, 2x status_changed" 4 "$(grep -o '"kind"' "$TMP/body" | wc -l | tr -d ' ')"

body=$(creds "$OTHER_EMAIL" "$PASSWORD")
call POST /v1/auth/register "$body" >/dev/null
call POST /v1/auth/login "$body" >/dev/null
OTHER_TOKEN="$(field token)"
expect "altro requester -> 404" 404 "$(call GET "/v1/tickets/$TICKET_ID" "" "$OTHER_TOKEN")"

echo "-- sessione"
expect "senza token -> 401" 401 "$(call GET /v1/me)"
expect "logout" 204 "$(call DELETE /v1/auth/session "" "$REQ_TOKEN")"
expect "token revocato -> 401" 401 "$(call GET /v1/me "" "$REQ_TOKEN")"

echo "== $pass ok, $fail falliti"
[[ $fail -eq 0 ]]
