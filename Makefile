# ledger-lab. Tareas de desarrollo y de campana experimental.

DATABASE_URL ?= postgres://postgres:postgres@127.0.0.1:5432/ledger
BASE_URL     ?= http://localhost:8080
PROFILE      ?= hot1
CONCURRENCY  ?= 50
DURATION     ?= 30s
SEED         ?= 1
LABEL        ?= go_ordered_locks_read_committed
KIND         ?= i5

export DATABASE_URL

.PHONY: help validate fmt vet test test-integration build run migrate seed reset \
        verify inject bench up down logs clean

help:
	@echo "validate          formato, vet, pruebas y compilacion de ambos modulos"
	@echo "migrate seed      aplicar esquema y semillas"
	@echo "run               levantar ledgerd contra DATABASE_URL"
	@echo "verify            ejecutar el verificador de invariantes"
	@echo "inject KIND=i5    inyectar una violacion deliberada"
	@echo "bench             campana de carga con PROFILE y CONCURRENCY"
	@echo "up down           entorno completo con Docker Compose"

fmt:
	cd go && gofmt -w ./cmd ./internal
	cd harness && gofmt -w ./cmd ./internal

vet:
	cd go && go vet ./...
	cd harness && go vet ./...

test:
	cd go && go test -race ./...
	cd harness && go test -race ./...

build:
	cd go && go build ./...
	cd harness && go build ./...

validate: fmt vet test build
	@echo "validacion completa"

migrate:
	psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -f schema/migrations/0001_init.up.sql

seed:
	psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -f schema/seeds/dev_accounts.sql

reset:
	psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -f schema/migrations/0001_init.down.sql
	$(MAKE) migrate seed

run:
	cd go && go run ./cmd/ledgerd -dsn "$(DATABASE_URL)"

verify:
	cd harness && go run ./cmd/verifier -dsn "$(DATABASE_URL)"

inject:
	cd harness && go run ./cmd/injector -dsn "$(DATABASE_URL)" -kind $(KIND) -yes

bench:
	mkdir -p experiments/raw
	cd harness && go run ./cmd/workload \
		-base-url "$(BASE_URL)" -dsn "$(DATABASE_URL)" \
		-profile $(PROFILE) -concurrency $(CONCURRENCY) -duration $(DURATION) \
		-seed $(SEED) -label "$(LABEL)" -fund 1000000 \
		-out-json ../experiments/raw/$(LABEL)_$(PROFILE)_$(CONCURRENCY).json \
		-out-csv  ../experiments/raw/$(LABEL)_$(PROFILE)_$(CONCURRENCY).csv

up:
	docker compose up --build -d
	@echo "esperando a ledgerd"
	@until curl -sf $(BASE_URL)/healthz >/dev/null; do sleep 1; done
	@echo "listo"

down:
	docker compose down -v

logs:
	docker compose logs -f

clean:
	rm -rf experiments/raw/*.json experiments/raw/*.csv
