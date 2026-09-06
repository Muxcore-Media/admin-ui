module github.com/Muxcore-Media/admin-ui

go 1.26.5

require (
	github.com/Muxcore-Media/backup-local v0.1.2
	github.com/Muxcore-Media/contracts-automation v0.1.1-0.20260824174909-b7b0cb83d8b3
	github.com/Muxcore-Media/contracts-media-admin v0.1.1-0.20260905225357-350de7622545
	github.com/Muxcore-Media/contracts-playback v0.1.0
	github.com/Muxcore-Media/contracts-scanner v0.1.0
	github.com/Muxcore-Media/core v0.5.8
	github.com/Muxcore-Media/core/sdk/go/client v0.5.8
	github.com/Muxcore-Media/jellyfin v0.3.0
	github.com/Muxcore-Media/media-custom-formats v0.1.1
	github.com/a-h/templ v0.3.1020
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.83.0
)

require github.com/Muxcore-Media/media-list-sync v0.1.1

require github.com/Muxcore-Media/playback-monitor v0.1.0

require github.com/Muxcore-Media/playback-guard v0.1.0

require github.com/Muxcore-Media/media-library-maintainer v0.1.12

require (
	github.com/Muxcore-Media/media-movies v0.1.9
	github.com/Muxcore-Media/media-music v0.3.0
	github.com/Muxcore-Media/media-subtitles v0.4.8
	github.com/Muxcore-Media/media-transcoder v0.3.0
)

require (
	github.com/Muxcore-Media/contracts-media v0.1.0 // indirect
	github.com/Muxcore-Media/core/pkg/contracts v0.5.8 // indirect
	github.com/Muxcore-Media/core/pkg/tenant v0.5.8 // indirect
)

require (
	github.com/Muxcore-Media/media-rename v0.2.1
	github.com/Muxcore-Media/media-root-folders v0.1.1
	github.com/Muxcore-Media/media-tvshows v0.1.9
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
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

replace github.com/Muxcore-Media/media-music => ../media-music

replace github.com/Muxcore-Media/media-transcoder => ../media-transcoder

replace github.com/Muxcore-Media/media-library-maintainer => ../media-library-maintainer

replace github.com/Muxcore-Media/playback-monitor => ../playback-monitor

replace github.com/Muxcore-Media/playback-guard => ../playback-guard

replace github.com/Muxcore-Media/media-list-sync => ../media-list-sync

replace github.com/Muxcore-Media/jellyfin => ../jellyfin

replace github.com/Muxcore-Media/contracts-media => ../contracts-media

replace github.com/Muxcore-Media/contracts-playback => ../contracts-playback

replace github.com/Muxcore-Media/core => ../core

replace github.com/Muxcore-Media/core/pkg/contracts => ../core/pkg/contracts

replace github.com/Muxcore-Media/core/pkg/tenant => ../core/pkg/tenant

replace github.com/Muxcore-Media/core/sdk/go/client => ../core/sdk/go/client

replace github.com/Muxcore-Media/contracts-scanner => ../contracts-scanner

replace github.com/Muxcore-Media/contracts-automation => ../contracts-automation

replace github.com/Muxcore-Media/contracts-metadata => ../contracts-metadata

replace github.com/Muxcore-Media/core/sdk/go/module => ../core/sdk/go/module
