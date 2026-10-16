.PHONY: proto proto-lint

proto:
	cd libs/masir-proto && buf generate

proto-lint:
	cd libs/masir-proto && buf lint && buf breaking --against '../../.git#subdir=libs/masir-proto,branch=master'
