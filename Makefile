.PHONY: proto proto-lint build snapshot gui gui-dev gui-windows gui-linux

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

# The desktop app, as a macOS app bundle in apps/masir-gui/dist with masir inside it: the
# daemon it starts, and the geo data beside it, in its bin folder. GUI_ARCH picks the
# architecture; the default is this machine's.
GUI_DIR := apps/masir-gui
GUI_VERSION := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' $(GUI_DIR)/mygo.json)
GUI_ARCH ?= $(shell go env GOARCH)
GUI_RES := $(GUI_DIR)/resources/darwin-$(GUI_ARCH)/bin

gui:
	@test -f build/geo/geoip.dat || sh scripts/fetch-geo.sh
	mkdir -p $(GUI_RES)
	GOOS=darwin GOARCH=$(GUI_ARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS) -X main.version=$(GUI_VERSION)" -o $(GUI_RES)/masir ./apps/masir
	cp build/geo/geoip.dat build/geo/geosite.dat $(GUI_RES)/
	cd $(GUI_DIR) && go tool mygo build -platform darwin/$(GUI_ARCH)
	@echo "Built $(GUI_DIR)/dist/darwin-$(GUI_ARCH)/Masir.app"

# Runs the desktop app from source, rebuilding as files change.
gui-dev: build
	cd $(GUI_DIR) && MASIR_BIN=$(CURDIR)/dist/masir go tool mygo dev

# The desktop app for Windows, built from any machine: apps/masir-gui/dist/windows-<arch>
# holds Masir.exe, with masir.exe and the geo data in its bin folder. The installer is made on Windows.
GUI_WIN_ARCH ?= amd64
GUI_WIN_RES := $(GUI_DIR)/resources/windows-$(GUI_WIN_ARCH)/bin

gui-windows:
	@test -f build/geo/geoip.dat || sh scripts/fetch-geo.sh
	mkdir -p $(GUI_WIN_RES)
	GOOS=windows GOARCH=$(GUI_WIN_ARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS) -X main.version=$(GUI_VERSION)" -o $(GUI_WIN_RES)/masir.exe ./apps/masir
	cp build/geo/geoip.dat build/geo/geosite.dat $(GUI_WIN_RES)/
	cd $(GUI_DIR) && go tool mygo build -platform windows/$(GUI_WIN_ARCH)
	@echo "Built $(GUI_DIR)/dist/windows-$(GUI_WIN_ARCH)"

# The desktop app for Linux, built from any machine: apps/masir-gui/dist/linux-<arch> holds
# the executable, a Debian package and a tarball, with masir and the geo data in its bin folder.
GUI_LINUX_ARCH ?= amd64
GUI_LINUX_RES := $(GUI_DIR)/resources/linux-$(GUI_LINUX_ARCH)/bin

gui-linux:
	@test -f build/geo/geoip.dat || sh scripts/fetch-geo.sh
	mkdir -p $(GUI_LINUX_RES)
	GOOS=linux GOARCH=$(GUI_LINUX_ARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS) -X main.version=$(GUI_VERSION)" -o $(GUI_LINUX_RES)/masir ./apps/masir
	cp build/geo/geoip.dat build/geo/geosite.dat $(GUI_LINUX_RES)/
	cd $(GUI_DIR) && go tool mygo build -platform linux/$(GUI_LINUX_ARCH)
	@echo "Built $(GUI_DIR)/dist/linux-$(GUI_LINUX_ARCH)"
