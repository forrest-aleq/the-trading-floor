package belief

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// Graph is the belief graph — trust/confidence per competence key.
// Ported from MARS BeliefGraphStore.
type Graph struct {
	mu     sync.RWMutex
	log    *slog.Logger
	states map[string]*model.CompetenceState
}

func NewGraph() *Graph {
	return &Graph{
		log:    slog.Default().With("component", "belief-graph"),
		states: make(map[string]*model.CompetenceState),
	}
}

// CompetenceKey generates the belief key: desk::capability::context::regime
func CompetenceKey(deskID, capability, context, regime string) string {
	return fmt.Sprintf("%s::%s::%s::%s", deskID, capability, context, regime)
}

// Get returns or creates a competence state
func (g *Graph) Get(key string) *model.CompetenceState {
	g.mu.RLock()
	state, exists := g.states[key]
	g.mu.RUnlock()

	if exists {
		return state
	}

	// Create new with MARS defaults
	g.mu.Lock()
	defer g.mu.Unlock()

	// Double-check after acquiring write lock
	if state, exists = g.states[key]; exists {
		return state
	}

	state = &model.CompetenceState{
		Key:        key,
		Trust:      0.55,
		Confidence: 0.35,
		Autonomy:   model.Restricted,
		UpdatedAt:  time.Now(),
	}
	g.states[key] = state
	return state
}

// ApplySuccess updates beliefs after a profitable trade.
// MARS formula: trust += (1-trust) * 0.025, confidence += 0.03
// Trading adaptation: magnitude-weighted
func (g *Graph) ApplySuccess(key string, magnitude float64) {
	g.Get(key)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.states[key]
	if state == nil {
		return
	}

	// Clamp magnitude to [0, 2]
	if magnitude > 2.0 {
		magnitude = 2.0
	}
	if magnitude < 0 {
		magnitude = 0
	}

	weight := 0.025 * magnitude
	state.Trust += (1 - state.Trust) * weight
	state.Confidence += 0.03 * magnitude
	if state.Confidence > 1.0 {
		state.Confidence = 1.0
	}
	state.SuccessCount++
	state.UpdatedAt = time.Now()
	state.Autonomy = state.InferAutonomy()

	g.log.Info("belief updated (success)",
		"key", key,
		"trust", state.Trust,
		"confidence", state.Confidence,
		"autonomy", state.Autonomy,
		"magnitude", magnitude,
	)
}

// ApplyFailure updates beliefs after a losing trade.
// MARS formula: trust -= trust * 0.075, confidence -= 0.04
// Trading adaptation: magnitude-weighted + moral asymmetry for boundary violations
func (g *Graph) ApplyFailure(key string, magnitude float64, boundaryViolation bool) {
	g.Get(key)

	g.mu.Lock()
	defer g.mu.Unlock()

	state := g.states[key]
	if state == nil {
		return
	}

	// Clamp magnitude
	if magnitude > 2.0 {
		magnitude = 2.0
	}
	if magnitude < 0 {
		magnitude = 0
	}

	// Boundary violations get 10x moral asymmetry
	multiplier := 1.0
	if boundaryViolation {
		multiplier = 10.0
	}

	weight := 0.075 * magnitude * multiplier
	state.Trust -= state.Trust * weight
	if state.Trust < 0 {
		state.Trust = 0
	}
	state.Confidence -= 0.04 * magnitude
	if state.Confidence < 0 {
		state.Confidence = 0
	}
	state.FailureCount++
	state.UpdatedAt = time.Now()
	state.Autonomy = state.InferAutonomy()

	g.log.Info("belief updated (failure)",
		"key", key,
		"trust", state.Trust,
		"confidence", state.Confidence,
		"autonomy", state.Autonomy,
		"magnitude", magnitude,
		"boundary_violation", boundaryViolation,
	)
}

// DecayAll applies periodic decay to all beliefs (anti-overfitting layer 5)
func (g *Graph) DecayAll(decayPct float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	factor := 1.0 - (decayPct / 100.0)
	for key, state := range g.states {
		state.Trust *= factor
		state.Confidence *= factor
		state.Autonomy = state.InferAutonomy()
		g.log.Debug("belief decayed", "key", key, "trust", state.Trust, "confidence", state.Confidence)
	}
}

// DropAutonomy forces all states in a regime back to reasoning mode (regime transition)
func (g *Graph) DropAutonomy(regime string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for key, state := range g.states {
		if state.Autonomy == model.Autonomous {
			state.Autonomy = model.Supervised
			g.log.Warn("autonomy dropped due to regime shift",
				"key", key,
				"regime", regime,
			)
		}
	}
}

// All returns all competence states
func (g *Graph) All() []*model.CompetenceState {
	g.mu.RLock()
	defer g.mu.RUnlock()

	states := make([]*model.CompetenceState, 0, len(g.states))
	for _, s := range g.states {
		states = append(states, s)
	}
	return states
}

// Stats returns summary of belief graph
func (g *Graph) Stats() GraphStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := GraphStats{Total: len(g.states)}
	for _, s := range g.states {
		switch s.Autonomy {
		case model.Autonomous:
			stats.Autonomous++
		case model.Supervised:
			stats.Supervised++
		case model.Restricted:
			stats.Restricted++
		}
	}
	return stats
}

type GraphStats struct {
	Total      int
	Autonomous int
	Supervised int
	Restricted int
}
