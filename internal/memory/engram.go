package memory

import (
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Engram is a cached winning play — a compressed action plan that proved successful.
// Layer 1: global (cross-desk). Layer 2: desk-specific.
type Engram struct {
	ID             string    `json:"id"`
	IntentKey      string    `json:"intent_key"`        // e.g. "earnings_straddle_low_iv"
	ContextPattern string    `json:"context_pattern"`   // matching conditions
	Capability     string    `json:"capability"`        // e.g. "options.straddle"
	DeskID         string    `json:"desk_id,omitempty"` // empty for Layer 1 (global)
	Layer          int       `json:"layer"`             // 1=global, 2=desk-specific
	SuccessCount   int       `json:"success_count"`
	FailureCount   int       `json:"failure_count"`
	AvgReturn      float64   `json:"avg_return"`
	Sharpe         float64   `json:"sharpe"`
	RegimeTags     []string  `json:"regime_tags"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// WinRate returns the success rate.
func (e *Engram) WinRate() float64 {
	total := e.SuccessCount + e.FailureCount
	if total == 0 {
		return 0
	}
	return float64(e.SuccessCount) / float64(total)
}

// TotalObservations returns the total number of observations.
func (e *Engram) TotalObservations() int {
	return e.SuccessCount + e.FailureCount
}

// EngramStore is an in-memory engram library.
type EngramStore struct {
	mu       sync.RWMutex
	log      *slog.Logger
	engrams  map[string]*Engram   // id -> engram
	byKey    map[string][]*Engram // intent_key -> engrams
	onChange func(*Engram)
}

func NewEngramStore() *EngramStore {
	return &EngramStore{
		log:     slog.Default().With("component", "engram-store"),
		engrams: make(map[string]*Engram),
		byKey:   make(map[string][]*Engram),
	}
}

func (s *EngramStore) SetChangeHandler(fn func(*Engram)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *EngramStore) Load(records []*Engram) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, incoming := range records {
		if incoming == nil || incoming.ID == "" {
			continue
		}
		engram := cloneEngram(incoming)
		s.engrams[engram.ID] = engram
		s.byKey[engram.IntentKey] = append(s.byKey[engram.IntentKey], engram)
	}
}

// Record records an outcome for a pattern. Creates or updates the engram.
func (s *EngramStore) Record(intentKey, contextPattern, capability, deskID string, regime []string, profitable bool, returnPct float64) {
	s.mu.Lock()
	layer := 1
	if deskID != "" {
		layer = 2
	}

	// Find existing engram matching this pattern
	lookupKey := intentKey + "::" + deskID
	var engram *Engram
	for _, e := range s.byKey[intentKey] {
		if e.DeskID == deskID && e.Capability == capability {
			engram = e
			break
		}
	}

	if engram == nil {
		engram = &Engram{
			ID:             uuid.New().String(),
			IntentKey:      intentKey,
			ContextPattern: contextPattern,
			Capability:     capability,
			DeskID:         deskID,
			Layer:          layer,
			RegimeTags:     regime,
			CreatedAt:      time.Now(),
		}
		s.engrams[engram.ID] = engram
		s.byKey[intentKey] = append(s.byKey[intentKey], engram)
		_ = lookupKey // suppress unused
	}

	if profitable {
		engram.SuccessCount++
	} else {
		engram.FailureCount++
	}

	// Rolling average return
	total := float64(engram.TotalObservations())
	engram.AvgReturn = engram.AvgReturn*(total-1)/total + returnPct/total
	engram.UpdatedAt = time.Now()

	// Merge regime tags
	for _, tag := range regime {
		found := false
		for _, existing := range engram.RegimeTags {
			if existing == tag {
				found = true
				break
			}
		}
		if !found {
			engram.RegimeTags = append(engram.RegimeTags, tag)
		}
	}
	changed := cloneEngram(engram)
	handler := s.onChange
	s.mu.Unlock()
	if handler != nil {
		handler(changed)
	}
}

// Lookup returns engrams matching an intent key, prioritizing desk-specific (Layer 2)
// while still falling back to global (Layer 1) playbooks when available.
func (s *EngramStore) Lookup(intentKey, deskID string) []*Engram {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.byKey[intentKey]
	if len(all) == 0 {
		return nil
	}

	// Desk-specific first, then global
	var deskEngrams, globalEngrams []*Engram
	for _, e := range all {
		if e.DeskID == deskID {
			deskEngrams = append(deskEngrams, e)
		} else if e.Layer == 1 {
			globalEngrams = append(globalEngrams, e)
		}
	}

	if len(deskEngrams) == 0 {
		return globalEngrams
	}

	combined := make([]*Engram, 0, len(deskEngrams)+len(globalEngrams))
	combined = append(combined, deskEngrams...)
	combined = append(combined, globalEngrams...)
	return combined
}

// All returns all engrams.
func (s *EngramStore) All() []*Engram {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Engram, 0, len(s.engrams))
	for _, e := range s.engrams {
		result = append(result, cloneEngram(e))
	}
	return result
}

// Stats returns summary statistics.
func (s *EngramStore) Stats() EngramStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := EngramStats{Total: len(s.engrams)}
	for _, e := range s.engrams {
		if e.Layer == 1 {
			stats.Global++
		} else {
			stats.DeskSpecific++
		}
		if e.TotalObservations() >= 10 {
			stats.Mature++
		}
	}
	return stats
}

type EngramStats struct {
	Total        int
	Global       int
	DeskSpecific int
	Mature       int // >= 10 observations
}

func cloneEngram(engram *Engram) *Engram {
	if engram == nil {
		return nil
	}
	cloned := *engram
	if len(engram.RegimeTags) > 0 {
		cloned.RegimeTags = append([]string(nil), engram.RegimeTags...)
	}
	return &cloned
}
