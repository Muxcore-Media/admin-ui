package handler

import (
	"testing"
	"time"
)

func TestMaintainerTimeoutsBound(t *testing.T) {
	if maintainerDialTimeout != 3*time.Second {
		t.Fatalf("maintainerDialTimeout: got %v, want 3s", maintainerDialTimeout)
	}
	if maintainerReadTimeout != 5*time.Second {
		t.Fatalf("maintainerReadTimeout: got %v, want 5s", maintainerReadTimeout)
	}
	pageBudget := maintainerDialTimeout + 6*maintainerReadTimeout + time.Second
	if maintainerPageTimeout != pageBudget {
		t.Fatalf("maintainerPageTimeout: got %v, want %v", maintainerPageTimeout, pageBudget)
	}
	if maintainerPageTimeout < maintainerDialTimeout+maintainerReadTimeout {
		t.Fatal("maintainer page budget must cover dial + at least one read")
	}
	actionBudget := maintainerDialTimeout + maintainerReadTimeout + time.Second
	if maintainerActionTimeout != actionBudget {
		t.Fatalf("maintainerActionTimeout: got %v, want %v", maintainerActionTimeout, actionBudget)
	}
}
