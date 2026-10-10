package templates

// UserErasureStatus is the /users view of the identity provider's erasure
// ledger status (ADR-0035 §5). It is built only from
// AuthService.GetUserErasureStatus; admin-ui keeps no second status source.
type UserErasureStatus struct {
	// Available is false when the provider reported no status (unsupported or
	// unreachable); Message then says why.
	Available bool
	Message   string
	// Rows lists incomplete erasures first, then the most recent complete ones.
	Rows []UserErasureRow
	// Truncated is true when the provider has more erasures than were read.
	Truncated bool
}

// UserErasureRow is one erased account. It carries no user id or username:
// the ledger does not either.
type UserErasureRow struct {
	ErasureID string
	DeletedAt string
	Complete  bool
	Modules   []UserErasureModule
}

// UserErasureModule is one module's acknowledgement for an erasure.
type UserErasureModule struct {
	ModuleID string
	// State is one of "ok", "pending", "failed", "unsupported".
	State string
	// Detail is the module's detail code, empty when none.
	Detail string
	// AckedAt is the last acknowledgement time (RFC 3339), empty when pending.
	AckedAt  string
	Required bool
}

// ErasureStateLabel is the visible text for a module state; colour is never
// the only signal.
func ErasureStateLabel(state string) string {
	switch state {
	case "ok":
		return "Done"
	case "failed":
		return "Failed"
	case "unsupported":
		return "Unsupported"
	default:
		return "Pending"
	}
}

func erasureStateClass(state string) string {
	switch state {
	case "ok":
		return "text-green-400"
	case "failed":
		return "text-red-400"
	case "unsupported":
		return "text-amber-300"
	default:
		return "text-gray-300"
	}
}

func erasureRowLabel(r UserErasureRow) string {
	if r.Complete {
		return "Complete"
	}
	return "Incomplete"
}
