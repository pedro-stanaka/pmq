.PHONY: build test lint run check
build:
	go build -o bin/pmq ./cmd/pmq
test:
	go test ./...
lint:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal scripts)" || (gofmt -l cmd internal scripts; exit 1)
run:
	go run ./cmd/pmq serve --dev
check: lint test build
