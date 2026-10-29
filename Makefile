-include .env
export

.PHONY: dev test migrate-up migrate-down migrate-new

# Run the API locally on :8080.
dev:
	go run ./cmd/dev

test:
	go test ./...

# Apply all pending migrations to $(MIGRATE_DATABASE_URL).
migrate-up:
	migrate -path ./migrations -database "$${MIGRATE_DATABASE_URL}" up

# Roll back the most recent migration.
migrate-down:
	migrate -path ./migrations -database "$${MIGRATE_DATABASE_URL}" down 1

# Create a new migration pair: make migrate-new name=create_rooms
migrate-new:
	migrate create -ext sql -dir ./migrations -seq $(name)
