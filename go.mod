module github.com/Muxcore-Media/admin-ui

go 1.26.3

require (
	github.com/Muxcore-Media/core v0.1.0
	github.com/Muxcore-Media/core/sdk/go/client v0.1.0
	github.com/a-h/templ v0.3.1020
	github.com/google/uuid v1.6.0
)

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts
