module github.com/narixius/masir-go/apps/masir-gui

go 1.27.3

require (
	github.com/egoist/mygo v0.7.3
	github.com/narixius/masir-go/libs/masir-core v0.0.0
	github.com/narixius/masir-go/libs/masir-proto v0.0.0
	golang.org/x/sys v0.49.0
	golang.org/x/text v0.43.0
	google.golang.org/grpc v1.85.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261026165753-2ffab32bce7e // indirect
)

replace (
	github.com/narixius/masir-go/libs/masir-core => ../../libs/masir-core
	github.com/narixius/masir-go/libs/masir-proto => ../../libs/masir-proto
)

tool github.com/egoist/mygo/cmd/mygo
