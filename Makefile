.PHONY: test vet sqlc build build-arm64 check
GO ?= go

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

# Regenerate internal/store/db from internal/store/queries (needs: go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest)
sqlc:
	sqlc generate

build:
	$(GO) build -o bin/ ./cmd/...

# Cross build for the Raspberry Pi (no CGO)
build-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/arpegiod ./cmd/arpegiod
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/arpegio ./cmd/arpegio

# What to run before every commit
check: sqlc vet test
	@test -z "$$(gofmt -l cmd internal)" || (echo "gofmt needed:"; gofmt -l cmd internal; exit 1)
