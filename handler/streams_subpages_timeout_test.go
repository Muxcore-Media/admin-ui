package handler

import (
	"testing"
	"time"
)

func TestStreamsSubpagesTimeoutsBound(t *testing.T) {
	if playbackGuardDialTimeout != 3*time.Second || playbackGuardReadTimeout != 5*time.Second {
		t.Fatalf("guard timeouts: dial=%v read=%v", playbackGuardDialTimeout, playbackGuardReadTimeout)
	}
	if playbackGuardPageTimeout < playbackGuardDialTimeout+3*playbackGuardReadTimeout {
		t.Fatalf("playbackGuardPageTimeout too small: %v", playbackGuardPageTimeout)
	}
	if playbackGuardActionTimeout != playbackGuardDialTimeout+playbackGuardReadTimeout+time.Second {
		t.Fatalf("playbackGuardActionTimeout: got %v", playbackGuardActionTimeout)
	}
	if streamsMapPageTimeout != playbackMonitorDialTimeout+playbackMonitorReadTimeout+time.Second {
		t.Fatalf("streamsMapPageTimeout: got %v", streamsMapPageTimeout)
	}
	if streamsServersPageTimeout != playbackMonitorDialTimeout+playbackMonitorReadTimeout+time.Second {
		t.Fatalf("streamsServersPageTimeout: got %v", streamsServersPageTimeout)
	}
	wantLibs := playbackMonitorDialTimeout + streamsLibrariesReads*playbackMonitorReadTimeout + time.Second
	if streamsLibrariesPageTimeout != wantLibs {
		t.Fatalf("streamsLibrariesPageTimeout: got %v want %v", streamsLibrariesPageTimeout, wantLibs)
	}
}
