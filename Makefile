BINARY := mb8611-poller
BUILD_DIR := bin
GOFLAGS := -trimpath -ldflags="-s -w"

.PHONY: build test lint verify clean install fixtures

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/mb8611-poller

test:
	go test -v -race ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found; running go vet instead"; \
		go vet ./...; \
	fi

# Aggregate phase-verification gate: gofmt check, then vet, then race tests.
# Non-zero exit on any failure.
verify:
	@test -z "$$(gofmt -l . | tee /dev/stderr)"
	go vet ./...
	go test -race ./... -count=1

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

# Minimal binary install. The systemd unit + config-file install belongs to
# Phase 7; do not add that logic here.
install: build
	install -d /usr/local/bin
	install -m 755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

# Requires a reachable modem and credentials supplied only through the environment.
# Example: MB8611_MODEM_HOST=... MB8611_MODEM_USERNAME=... MB8611_MODEM_PASSWORD=... make fixtures
fixtures:
	@test -n "$$MB8611_MODEM_HOST" && test -n "$$MB8611_MODEM_USERNAME" && test -n "$$MB8611_MODEM_PASSWORD" || (echo "set MB8611_MODEM_HOST, MB8611_MODEM_USERNAME, and MB8611_MODEM_PASSWORD" >&2; exit 2)
	go run ./cmd/dump-fixtures --dir internal/hnap/hnaptest/testdata
	./scripts/scrub-fixtures.sh internal/hnap/hnaptest/testdata
