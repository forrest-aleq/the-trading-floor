package firm

import "time"

type EntryMode string

const (
	EntryModeNormal          EntryMode = "normal"
	EntryModeEntriesDisabled EntryMode = "entries_disabled"
)

// EntryPolicy is the current runtime control state governing whether desks may
// open new entries. Exits intentionally bypass this policy.
type EntryPolicy struct {
	Mode         EntryMode `json:"mode"`
	AllowEntries bool      `json:"allow_entries"`
	Reason       string    `json:"reason,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// EntryControl exposes the live runtime policy for new entries.
type EntryControl interface {
	CurrentEntryPolicy() EntryPolicy
}

func NormalEntryPolicy(at time.Time) EntryPolicy {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return EntryPolicy{
		Mode:         EntryModeNormal,
		AllowEntries: true,
		UpdatedAt:    at,
	}
}

func DisabledEntryPolicy(reason string, at time.Time) EntryPolicy {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return EntryPolicy{
		Mode:         EntryModeEntriesDisabled,
		AllowEntries: false,
		Reason:       reason,
		UpdatedAt:    at,
	}
}
