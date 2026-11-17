.PHONY: build test lint run check docs-dev docs-build
# Development builds embed a git-derived version; release builds are stamped
# by GoReleaser and the image workflow instead.
PMQ_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
build:
	go build -ldflags "-X main.version=$(PMQ_VERSION)" -o bin/pmq ./cmd/pmq
test:
	go test ./...
lint:
	go vet ./...
	@test -z "$$(gofmt -l cmd internal scripts)" || (gofmt -l cmd internal scripts; exit 1)
run:
	go run ./cmd/pmq serve --dev
check: lint test build
# Documentation website (docs/site). Install dependencies first with
# `npm ci --prefix docs/site`. Deploys to GitHub Pages from main via
# .github/workflows/docs.yml.
docs-dev:
	npm run dev --prefix docs/site
docs-build:
	npm run build --prefix docs/site
