-include .env
export

.PHONY: test fmt vet lint tidy migrate seed

test:
	@go test ./...

fmt:
	@gofmt -w .

vet:
	@go vet ./...

lint:
	@golangci-lint run ./...

tidy:
	@go mod tidy

migrate:
	@go run ./cmd/migrate

seed:
	@go run ./cmd/seed
