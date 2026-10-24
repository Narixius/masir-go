.PHONY: proto proto-lint build

proto:
	cd libs/masir-proto && buf generate

proto-lint:
	cd libs/masir-proto && buf lint && buf breaking --against '../../.git#subdir=libs/masir-proto,branch=master'

build:
	go build -o ./dist/masird ./apps/masird
	go build -o ./dist/masir ./apps/masir
