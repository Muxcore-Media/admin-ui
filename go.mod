module github.com/Muxcore-Media/admin-ui

go 1.26.4

require (
	github.com/Muxcore-Media/backup-local v0.1.2
	github.com/Muxcore-Media/contracts-media-admin v0.1.0
	github.com/Muxcore-Media/core v0.5.2
	github.com/Muxcore-Media/core/sdk/go/client v0.5.2
	github.com/Muxcore-Media/jellyfin v0.2.1
	github.com/Muxcore-Media/media-automation v0.1.5
	github.com/Muxcore-Media/media-custom-formats v0.1.1
	github.com/a-h/templ v0.3.1020
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.83.0
)

require github.com/Muxcore-Media/media-list-sync v0.1.1

require (
	github.com/Muxcore-Media/media-movies v0.1.9
	github.com/Muxcore-Media/media-scanner v0.1.1
	github.com/Muxcore-Media/media-subtitles v0.4.8
)

require (
	github.com/Muxcore-Media/media-rename v0.2.1
	github.com/Muxcore-Media/media-root-folders v0.1.1
	github.com/Muxcore-Media/media-tvshows v0.1.9
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/protobuf v1.36.11
)

replace github.com/Muxcore-Media/media-tvshows => ../media-tvshows

replace github.com/Muxcore-Media/media-automation => ../media-automation

replace github.com/Muxcore-Media/media-subtitles => ../media-subtitles

replace github.com/Muxcore-Media/media-custom-formats => ../media-custom-formats

replace github.com/Muxcore-Media/backup-local => ../backup-local

replace github.com/Muxcore-Media/media-scanner => ../media-scanner

replace github.com/Muxcore-Media/media-movies => ../media-movies

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts
