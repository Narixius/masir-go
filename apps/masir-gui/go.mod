module github.com/narixius/masir-go/apps/masir-gui

go 1.27.3

require (
	github.com/egoist/mygo v0.6.5-0.20261026162610-43199d0b0fb9
	github.com/narixius/masir-go/libs/masir-core v0.0.0
	github.com/narixius/masir-go/libs/masir-proto v0.0.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	golang.org/x/net v0.59.0 // indirect
)

tool github.com/egoist/mygo/cmd/mygo
