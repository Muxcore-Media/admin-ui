module github.com/Muxcore-Media/admin-ui

go 1.26.4

require (
	github.com/Muxcore-Media/contracts-media-admin v0.1.0
	github.com/Muxcore-Media/core v0.4.0
	github.com/Muxcore-Media/core/sdk/go/client v0.1.0
	github.com/Muxcore-Media/media-custom-formats v0.1.0
	github.com/a-h/templ v0.3.1020
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.81.1
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Muxcore-Media/contracts-media-admin => ../contracts-media-admin

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts

replace github.com/Muxcore-Media/media-custom-formats => ../media-custom-formats
