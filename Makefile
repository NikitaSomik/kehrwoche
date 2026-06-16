-include .env
export

.PHONY: run build test fmt vet tidy send

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

tidy:
	@go mod tidy

send:
	@SEND_NOW=1 go run .
