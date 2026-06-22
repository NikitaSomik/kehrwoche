-include .env
export

.PHONY: test fmt vet lint tidy

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
