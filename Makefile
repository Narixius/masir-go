.PHONY: proto proto-lint build snapshot

proto:
	cd libs/masir-proto && buf generate

proto-lint:
	cd libs/masir-proto && buf lint && buf breaking --against '../../.git#subdir=libs/masir-proto,branch=master'

build:
	go build -o ./dist/masird ./apps/masird
	go build -o ./dist/masir ./apps/masir

# All release archives, as CI builds them, into build/release (needs network for geo data).
snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean
