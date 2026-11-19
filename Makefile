BINARY := mb8611-poller
BUILD_DIR := bin
GOFLAGS := -trimpath -ldflags="-s -w"
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Local tag; pass IMAGE=<registry>/<repo>:<tag> to build for a registry.
IMAGE ?= localhost/mb8611-poller:$(VERSION)

.PHONY: build test lint verify clean install fixtures image

build:
	install -d $(BUILD_DIR)
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

# Build the runtime container image locally (does not push).
# Override the tag with: make image IMAGE=localhost/mb8611-poller:test
image:
	podman build -f deploy/Containerfile -t $(IMAGE) .

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

install:
	@test -x $(BUILD_DIR)/$(BINARY) || (echo "$(BUILD_DIR)/$(BINARY) is missing; run 'make build' as your normal user first" >&2; exit 2)
	install -d /usr/local/bin /etc/mb8611-poller /etc/systemd/system
	install -m 755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)
	test -f /etc/mb8611-poller/config.yaml || install -o root -g mb8611-poller -m 640 config.yaml.example /etc/mb8611-poller/config.yaml
	chown root:mb8611-poller /etc/mb8611-poller/config.yaml
	chmod 640 /etc/mb8611-poller/config.yaml
	install -m 644 mb8611-poller.service /etc/systemd/system/mb8611-poller.service
	systemctl daemon-reload

# Requires a reachable modem and credentials supplied only through the environment.
# Example: MB8611_MODEM_HOST=... MB8611_MODEM_USERNAME=... MB8611_MODEM_PASSWORD=... make fixtures
fixtures:
	@test -n "$$MB8611_MODEM_HOST" && test -n "$$MB8611_MODEM_USERNAME" && test -n "$$MB8611_MODEM_PASSWORD" || (echo "set MB8611_MODEM_HOST, MB8611_MODEM_USERNAME, and MB8611_MODEM_PASSWORD" >&2; exit 2)
	go run ./cmd/dump-fixtures --dir internal/hnap/hnaptest/testdata
	./scripts/scrub-fixtures.sh internal/hnap/hnaptest/testdata
