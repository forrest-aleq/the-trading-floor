.PHONY: build run test lint clean ctl backfill migrate

BINARY=trading-floor
CMD_DIR=./cmd

build:
	go build -o bin/floor $(CMD_DIR)/floor
	go build -o bin/backfill $(CMD_DIR)/backfill
	go build -o bin/ctl $(CMD_DIR)/ctl

run: build
	./bin/floor

test:
	go test ./... -v -race

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

ctl: build
	./bin/ctl $(ARGS)

backfill: build
	./bin/backfill $(ARGS)

migrate:
	psql $(DATABASE_URL) -f store/migrations/001_init.sql

docker:
	docker build -t trading-floor -f deploy/docker/Dockerfile .

# Development helpers
dev-deps:
	docker compose -f deploy/docker/docker-compose.yml up -d postgres redis neo4j

kill:
	./bin/ctl kill-switch

status:
	./bin/ctl status

desks:
	./bin/ctl desks

pnl:
	./bin/ctl pnl
