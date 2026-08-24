package handler

import (
	"testing"
	"time"
)

func TestClusterStorageSettingsAuditTimeoutsBound(t *testing.T) {
	if clusterDialTimeout != 3*time.Second || clusterReadTimeout != 5*time.Second {
		t.Fatalf("cluster timeouts: dial=%v read=%v", clusterDialTimeout, clusterReadTimeout)
	}
	if clusterPageTimeout != clusterDialTimeout+clusterReadTimeout+time.Second {
		t.Fatalf("clusterPageTimeout: got %v", clusterPageTimeout)
	}
	if storageDialTimeout != 3*time.Second || storageReadTimeout != 5*time.Second {
		t.Fatalf("storage timeouts: dial=%v read=%v", storageDialTimeout, storageReadTimeout)
	}
	if storagePageTimeout != storageDialTimeout+2*storageReadTimeout+time.Second {
		t.Fatalf("storagePageTimeout: got %v", storagePageTimeout)
	}
	if settingsDialTimeout != 3*time.Second || settingsReadTimeout != 5*time.Second {
		t.Fatalf("settings timeouts: dial=%v read=%v", settingsDialTimeout, settingsReadTimeout)
	}
	if settingsPageTimeout < settingsDialTimeout+settingsReadTimeout {
		t.Fatal("settings page budget too small")
	}
	if auditDialTimeout != 3*time.Second || auditReadTimeout != 5*time.Second {
		t.Fatalf("audit timeouts: dial=%v read=%v", auditDialTimeout, auditReadTimeout)
	}
	if auditPageTimeout != auditDialTimeout+auditReadTimeout+time.Second {
		t.Fatalf("auditPageTimeout: got %v", auditPageTimeout)
	}
}
