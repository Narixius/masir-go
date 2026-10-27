.PHONY: proto proto-lint build snapshot gui gui-dev

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

# Runs the desktop app from source, rebuilding as files change.
gui-dev: build
	cd $(GUI_DIR) && MASIR_BIN=$(CURDIR)/dist/masir go tool mygo dev
