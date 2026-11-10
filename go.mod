module boxpilot

go 1.27.3

require (
	connectrpc.com/connect v1.21.1
	github.com/egoist/mygo v0.0.0
	github.com/tailscale/hujson v0.0.0-20260727124030-b80ff77dac4f
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/egoist/mygo => ../mygo

tool github.com/egoist/mygo/cmd/mygo
