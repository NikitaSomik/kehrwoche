-include .env
export

.PHONY: run build test fmt vet lint tidy send

run:
	@go run .

build:
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o out/main .

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

send:
	@SEND_NOW=1 go run .
