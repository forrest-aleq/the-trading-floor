package belief

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/model"
)

// Graph is the belief graph — trust/confidence per competence key.
// Ported from MARS BeliefGraphStore.
type Graph struct {
	mu           sync.RWMutex
	log          *slog.Logger
	states       map[string]*model.CompetenceState
	peerStates   map[string]*model.DeskRelationshipBelief
	onChange     func(*model.CompetenceState)
	onPeerChange func(*model.DeskRelationshipBelief)
}

func NewGraph() *Graph {
	return &Graph{
		log:        slog.Default().With("component", "belief-graph"),
		states:     make(map[string]*model.CompetenceState),
		peerStates: make(map[string]*model.DeskRelationshipBelief),
	}
}

// CompetenceKey generates the belief key: desk::capability::context::regime
func CompetenceKey(deskID, capability, context, regime string) string {
	return fmt.Sprintf("%s::%s::%s::%s", deskID, capability, context, regime)
}

func ParseCompetenceKey(key string) (deskID, capability, context, regime string) {
	parts := strings.SplitN(key, "::", 4)
	if len(parts) != 4 {
		return "", "", "", ""
	}
	return parts[0], parts[1], parts[2], parts[3]
}

func PeerBeliefKey(originDesk, receivingDesk, domain, regime string) string {
	return fmt.Sprintf("%s::%s::%s::%s", originDesk, receivingDesk, contextOrUnknown(domain), regime)
}

func ParsePeerBeliefKey(key string) (originDesk, receivingDesk, domain, regime string) {
	parts := strings.SplitN(key, "::", 4)
	if len(parts) != 4 {
		return "", "", "", ""
	}
	return parts[0], parts[1], parts[2], parts[3]
}

type TerritoryStatus string

const (
	TerritoryUnknown  TerritoryStatus = "unknown"
	TerritoryAdjacent TerritoryStatus = "adjacent"
	TerritoryKnown    TerritoryStatus = "known"
)

type TerritoryAssessment struct {
	Status   TerritoryStatus
	Exact    *model.CompetenceState
	Adjacent []*model.CompetenceState
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
	state.DeskID, state.Capability, state.Context, state.Regime = ParseCompetenceKey(key)
	g.states[key] = state
	return state
}

func (g *Graph) SetChangeHandler(fn func(*model.CompetenceState)) {
	g.mu.Lock()
	g.onChange = fn
	g.mu.Unlock()
}

func (g *Graph) SetPeerChangeHandler(fn func(*model.DeskRelationshipBelief)) {
	g.mu.Lock()
	g.onPeerChange = fn
	g.mu.Unlock()
}

func (g *Graph) Load(states []*model.CompetenceState) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, incoming := range states {
		if incoming == nil || incoming.Key == "" {
			continue
		}
		state := cloneState(incoming)
		if state.DeskID == "" || state.Capability == "" || state.Regime == "" {
			state.DeskID, state.Capability, state.Context, state.Regime = ParseCompetenceKey(state.Key)
		}
		g.states[state.Key] = state
	}
}

func (g *Graph) Lookup(deskID, capability, context, regime string) (*model.CompetenceState, bool) {
	key := CompetenceKey(deskID, capability, context, regime)
	g.mu.RLock()
	defer g.mu.RUnlock()

	state, ok := g.states[key]
	if !ok {
		return nil, false
	}
	return cloneState(state), true
}

func (g *Graph) GetPeer(key string) *model.DeskRelationshipBelief {
	g.mu.RLock()
	state, exists := g.peerStates[key]
	g.mu.RUnlock()
	if exists {
		return clonePeerState(state)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if state, exists = g.peerStates[key]; exists {
		return clonePeerState(state)
	}
	originDesk, receivingDesk, domain, regime := ParsePeerBeliefKey(key)
	state = &model.DeskRelationshipBelief{
		Key:           key,
		OriginDesk:    originDesk,
		ReceivingDesk: receivingDesk,
		Domain:        domain,
		Regime:        regime,
		Trust:         0.55,
		Confidence:    0.35,
		UpdatedAt:     time.Now(),
	}
	g.peerStates[key] = state
	return clonePeerState(state)
}

func (g *Graph) LookupPeer(originDesk, receivingDesk, domain, regime string) (*model.DeskRelationshipBelief, bool) {
	key := PeerBeliefKey(originDesk, receivingDesk, domain, regime)
	g.mu.RLock()
	defer g.mu.RUnlock()
	state, ok := g.peerStates[key]
	if !ok {
		return nil, false
	}
	return clonePeerState(state), true
}

func (g *Graph) AssessTerritory(deskID, capability, context, regime string, minObservations int) TerritoryAssessment {
	key := CompetenceKey(deskID, capability, context, regime)
	g.mu.RLock()
	defer g.mu.RUnlock()

	var exact *model.CompetenceState
	var adjacent []*model.CompetenceState

	for stateKey, state := range g.states {
		if state == nil {
			continue
		}

		sDesk, sCapability, sContext, sRegime := state.DeskID, state.Capability, state.Context, state.Regime
		if sDesk == "" || sCapability == "" || sRegime == "" {
			sDesk, sCapability, sContext, sRegime = ParseCompetenceKey(stateKey)
		}

		if sDesk != deskID || sCapability != capability || sContext != context {
			continue
		}

		if stateKey == key || sRegime == regime {
			exact = cloneState(state)
			continue
		}
		if state.TotalObservations() > 0 {
			adjacent = append(adjacent, cloneState(state))
		}
	}

	if exact != nil && exact.TotalObservations() >= minObservations {
		return TerritoryAssessment{
			Status:   TerritoryKnown,
			Exact:    exact,
			Adjacent: adjacent,
		}
	}

	if len(adjacent) > 0 {
		return TerritoryAssessment{
			Status:   TerritoryAdjacent,
			Exact:    exact,
			Adjacent: adjacent,
		}
	}

	return TerritoryAssessment{
		Status:   TerritoryUnknown,
		Exact:    exact,
		Adjacent: adjacent,
	}
}

// ApplySuccess updates beliefs after a profitable trade.
// MARS formula: trust += (1-trust) * 0.025, confidence += 0.03
// Trading adaptation: magnitude-weighted
func (g *Graph) ApplySuccess(key string, magnitude float64) {
	g.Get(key)

	g.mu.Lock()
	state := g.states[key]
	if state == nil {
		g.mu.Unlock()
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
	changed := cloneState(state)
	handler := g.onChange
	g.mu.Unlock()

	g.log.Info("belief updated (success)",
		"key", key,
		"trust", changed.Trust,
		"confidence", changed.Confidence,
		"autonomy", changed.Autonomy,
		"magnitude", magnitude,
	)
	if handler != nil {
		handler(changed)
	}
}

// ApplyFailure updates beliefs after a losing trade.
// MARS formula: trust -= trust * 0.075, confidence -= 0.04
// Trading adaptation: magnitude-weighted + moral asymmetry for boundary violations
func (g *Graph) ApplyFailure(key string, magnitude float64, boundaryViolation bool) {
	g.Get(key)

	g.mu.Lock()
	state := g.states[key]
	if state == nil {
		g.mu.Unlock()
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
	changed := cloneState(state)
	handler := g.onChange
	g.mu.Unlock()

	g.log.Info("belief updated (failure)",
		"key", key,
		"trust", changed.Trust,
		"confidence", changed.Confidence,
		"autonomy", changed.Autonomy,
		"magnitude", magnitude,
		"boundary_violation", boundaryViolation,
	)
	if handler != nil {
		handler(changed)
	}
}

func (g *Graph) ApplyPeerSuccess(key string, magnitude float64) {
	g.GetPeer(key)

	g.mu.Lock()
	state := g.peerStates[key]
	if state == nil {
		g.mu.Unlock()
		return
	}
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
	changed := clonePeerState(state)
	handler := g.onPeerChange
	g.mu.Unlock()

	if handler != nil {
		handler(changed)
	}
}

func (g *Graph) ApplyPeerFailure(key string, magnitude float64) {
	g.GetPeer(key)

	g.mu.Lock()
	state := g.peerStates[key]
	if state == nil {
		g.mu.Unlock()
		return
	}
	if magnitude > 2.0 {
		magnitude = 2.0
	}
	if magnitude < 0 {
		magnitude = 0
	}
	weight := 0.075 * magnitude
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
	changed := clonePeerState(state)
	handler := g.onPeerChange
	g.mu.Unlock()

	if handler != nil {
		handler(changed)
	}
}

// DecayAll applies periodic decay to all beliefs (anti-overfitting layer 5)
func (g *Graph) DecayAll(decayPct float64) {
	g.mu.Lock()
	factor := 1.0 - (decayPct / 100.0)
	changed := make([]*model.CompetenceState, 0, len(g.states))
	for key, state := range g.states {
		state.Trust *= factor
		state.Confidence *= factor
		state.Autonomy = state.InferAutonomy()
		g.log.Debug("belief decayed", "key", key, "trust", state.Trust, "confidence", state.Confidence)
		changed = append(changed, cloneState(state))
	}
	handler := g.onChange
	g.mu.Unlock()
	if handler != nil {
		for _, state := range changed {
			handler(state)
		}
	}
}

// DropAutonomy forces all states in a regime back to reasoning mode (regime transition)
func (g *Graph) DropAutonomy(regime string) {
	g.mu.Lock()
	changed := make([]*model.CompetenceState, 0)
	for key, state := range g.states {
		if state.Autonomy == model.Autonomous {
			state.Autonomy = model.Supervised
			g.log.Warn("autonomy dropped due to regime shift",
				"key", key,
				"regime", regime,
			)
			changed = append(changed, cloneState(state))
		}
	}
	handler := g.onChange
	g.mu.Unlock()
	if handler != nil {
		for _, state := range changed {
			handler(state)
		}
	}
}

// All returns all competence states
func (g *Graph) All() []*model.CompetenceState {
	g.mu.RLock()
	defer g.mu.RUnlock()

	states := make([]*model.CompetenceState, 0, len(g.states))
	for _, s := range g.states {
		states = append(states, cloneState(s))
	}
	return states
}

func (g *Graph) AllPeerBeliefs() []*model.DeskRelationshipBelief {
	g.mu.RLock()
	defer g.mu.RUnlock()
	states := make([]*model.DeskRelationshipBelief, 0, len(g.peerStates))
	for _, s := range g.peerStates {
		states = append(states, clonePeerState(s))
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

func cloneState(state *model.CompetenceState) *model.CompetenceState {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func clonePeerState(state *model.DeskRelationshipBelief) *model.DeskRelationshipBelief {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func contextOrUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
