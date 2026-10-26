.PHONY: proto proto-lint build snapshot

ANALYTICS_PKG := github.com/narixius/masir-go/libs/masir-daemon/internal/analytics
-include .env
export MASIR_POSTHOG_KEY MASIR_POSTHOG_HOST MASIR_UPDATE_PUBKEY
UPDATE_PKG := github.com/narixius/masir-go/libs/masir-daemon/internal/update
LDFLAGS := -X $(ANALYTICS_PKG).apiKey=$(MASIR_POSTHOG_KEY) -X $(ANALYTICS_PKG).host=$(MASIR_POSTHOG_HOST) -X $(UPDATE_PKG).publicKeys=$(MASIR_UPDATE_PUBKEY)

proto:
	cd libs/masir-proto && buf generate

proto-lint:
	cd libs/masir-proto && buf lint && buf breaking --against '../../.git#subdir=libs/masir-proto,branch=master'

# masir is a single executable: `masir daemon run` is the daemon.
build:
	go build -ldflags "$(LDFLAGS)" -o ./dist/masir ./apps/masir

# All release archives, as CI builds them, into build/release (needs network for geo data).
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
