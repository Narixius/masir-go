.PHONY: proto proto-lint build snapshot test test-race gui gui-local gui-dev snapshots

# The Go modules of go.work, in the order CI checks them.
MODULES := apps/masir apps/masir-gui libs/masir-daemon libs/masir-core libs/masir-proto

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

# The module checks CI runs, one module at a time. `test-race` adds the race detector.
test:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go vet ./... && go test ./...) || exit 1; \
	done

# The same with the race detector, for the final check before committing.
test-race:
	@for m in $(MODULES); do \
		echo "==> $$m"; \
		(cd $$m && go vet ./... && go test -race ./...) || exit 1; \
	done

# Pictures of the desktop app's pages, for looking at them without launching it.
SNAPSHOT_DIR ?= $(CURDIR)/dist/snapshots
snapshots:
	@mkdir -p "$(SNAPSHOT_DIR)"
	cd $(GUI_DIR) && MASIR_SNAPSHOTS="$(SNAPSHOT_DIR)" go test -count=1 -run TestSnapshots -v
	@echo "Snapshots in $(SNAPSHOT_DIR)"

# The desktop app, with the masir it ships, for the systems and architectures chosen.
# By default it builds all three systems and both architectures. Narrow it with either
# filter, or both for one build:
#
#   make gui
#   make gui GUI_OS=darwin
#   make gui GUI_ARCHES=arm64
#   make gui GUI_OS=windows GUI_ARCHES=arm64
#
# Outputs go to apps/masir-gui/dist/<os>-<arch>.
GUI_DIR := apps/masir-gui
GUI_VERSION := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' $(GUI_DIR)/mygo.json)
GUI_OS ?= darwin linux windows
GUI_ARCHES ?= amd64 arm64

gui:
	@GUI_OS="$(GUI_OS)" GUI_ARCHES="$(GUI_ARCHES)" GUI_VERSION="$(GUI_VERSION)" sh scripts/build-gui.sh

# The desktop app for this machine only: the fast build while developing.
GUI_LOCAL_OS := $(shell go env GOOS)
GUI_LOCAL_ARCH := $(shell go env GOARCH)
gui-local:
	@GUI_OS="$(GUI_LOCAL_OS)" GUI_ARCHES="$(GUI_LOCAL_ARCH)" GUI_VERSION="$(GUI_VERSION)" sh scripts/build-gui.sh

# Runs the desktop app from source, rebuilding as files change.
gui-dev: build
	cd $(GUI_DIR) && MASIR_BIN=$(CURDIR)/dist/masir go tool mygo dev
