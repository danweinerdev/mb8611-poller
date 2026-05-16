BINARY := mb8611-poller
BUILD_DIR := bin
GOFLAGS := -trimpath -ldflags="-s -w"

# go-monitor is a private module; without this every recipe that touches the
# module graph fails. Exported here so all recipes inherit it automatically.
export GOPRIVATE := github.com/danweinerdev/*

.PHONY: build test lint verify clean install

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
