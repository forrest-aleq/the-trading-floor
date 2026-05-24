package firm

import (
	"sync"
	"time"
)

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

type ManualEntryControl struct {
	mu     sync.RWMutex
	policy EntryPolicy
}

func NewManualEntryControl(initial EntryPolicy) *ManualEntryControl {
	if initial.Mode == "" {
		initial = NormalEntryPolicy(time.Now().UTC())
	}
	return &ManualEntryControl{policy: initial}
}

func (c *ManualEntryControl) CurrentEntryPolicy() EntryPolicy {
	if c == nil {
		return NormalEntryPolicy(time.Now().UTC())
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.policy.Mode == "" {
		return NormalEntryPolicy(time.Now().UTC())
	}
	return c.policy
}

func (c *ManualEntryControl) Set(policy EntryPolicy) {
	if c == nil {
		return
	}
	if policy.Mode == "" {
		policy = NormalEntryPolicy(time.Now().UTC())
	}
	if policy.UpdatedAt.IsZero() {
		policy.UpdatedAt = time.Now().UTC()
	}
	c.mu.Lock()
	c.policy = policy
	c.mu.Unlock()
}

func (c *ManualEntryControl) Disable(reason string, at time.Time) {
	c.Set(DisabledEntryPolicy(reason, at))
}

func (c *ManualEntryControl) Enable(at time.Time) {
	c.Set(NormalEntryPolicy(at))
}

type CombinedEntryControl struct {
	controls []EntryControl
}

func NewCombinedEntryControl(controls ...EntryControl) EntryControl {
	filtered := make([]EntryControl, 0, len(controls))
	for _, control := range controls {
		if control != nil {
			filtered = append(filtered, control)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return CombinedEntryControl{controls: filtered}
	}
}

func (c CombinedEntryControl) CurrentEntryPolicy() EntryPolicy {
	var newestNormal EntryPolicy
	for _, control := range c.controls {
		if control == nil {
			continue
		}
		policy := control.CurrentEntryPolicy()
		if policy.Mode == "" {
			continue
		}
		if !policy.AllowEntries {
			return policy
		}
		if newestNormal.Mode == "" || policy.UpdatedAt.After(newestNormal.UpdatedAt) {
			newestNormal = policy
		}
	}
	if newestNormal.Mode != "" {
		return newestNormal
	}
	return NormalEntryPolicy(time.Now().UTC())
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
