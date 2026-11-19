# Containerfile — mb8611-poller Go development image for the OpenCode2 sandbox.
#
# Layered onto the local OpenCode2 base image; .opencode-sandbox.json points
# the launcher at this file, so `opencode-container` builds it on first use
# (the base image must already exist locally).
#
# Adds what the repository's Makefile needs:
#   - Go, pinned to the exact version in go.mod (official tarball, sha256
#     verified). GOTOOLCHAIN=local stops `go` from silently downloading a
#     different toolchain.
#   - gcc, because `make test` / `make verify` run `go test -race`, which
#     requires cgo.
#   - make.
#
# No project source is copied in: the workspace is bind-mounted at runtime.
# Module and build caches live under /cache/go, which .opencode-sandbox.json
# bind-mounts from .cache/go in the workspace so they survive across runs.

ARG BASE_IMAGE=opencode2:latest
FROM ${BASE_IMAGE}

# The launcher always passes these build args; declare them so the build does
# not warn about unused args and so cache dirs can be chowned to the user.
ARG USER_UID=1000
ARG USER_GID=1000
ARG USERNAME=dev
ARG LOCAL_PROVIDERS_SHA256=absent

# Keep in sync with the `go` directive in go.mod.
ARG GO_VERSION=1.26.11
ARG GO_SHA256_AMD64=9849f4e04ee35303714e34fbc3ac8ab44ea3e448a95aa182f91e3b9b79c710ec
ARG GO_SHA256_ARM64=6c2f29b458b5add0835f18b776f69c8881a6d88428f1484a6da55755e620757e

USER root

RUN set -eux; \
    dnf install -y --setopt=install_weak_deps=False gcc make; \
    dnf clean all; \
    rm -rf /var/cache/dnf

RUN set -eux; \
    case "$(uname -m)" in \
        x86_64)  goarch=amd64; sha="${GO_SHA256_AMD64}" ;; \
        aarch64) goarch=arm64; sha="${GO_SHA256_ARM64}" ;; \
        *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;; \
    esac; \
    tarball="go${GO_VERSION}.linux-${goarch}.tar.gz"; \
    curl -fsSL -o "/tmp/${tarball}" "https://go.dev/dl/${tarball}"; \
    echo "${sha}  /tmp/${tarball}" | sha256sum -c -; \
    tar -C /usr/local -xzf "/tmp/${tarball}"; \
    rm -f "/tmp/${tarball}"

# Packages installed here must not widen the base's setuid policy.
RUN /usr/local/libexec/opencode-setuid-policy "$(cat /etc/opencode-nested-podman)"

ENV GOTOOLCHAIN=local \
    GOPATH=/cache/go \
    GOMODCACHE=/cache/go/pkg/mod \
    GOCACHE=/cache/go/build \
    PATH=/cache/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

RUN install -d -m 0755 -o "${USER_UID}" -g "${USER_GID}" \
        /cache /cache/go /cache/go/bin /cache/go/pkg /cache/go/build

LABEL io.opencode.local-providers-sha256="${LOCAL_PROVIDERS_SHA256}"

# Smoke check: the pinned toolchain and the race detector prerequisites.
RUN go version | grep -q "go${GO_VERSION} " && gcc --version >/dev/null && make --version >/dev/null

USER ${USER_UID}:${USER_GID}
