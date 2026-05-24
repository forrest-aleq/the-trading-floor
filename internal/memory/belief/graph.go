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
	mu               sync.RWMutex
	log              *slog.Logger
	states           map[string]*model.CompetenceState
	peerStates       map[string]*model.DeskRelationshipBelief
	sourceStates     map[string]*model.SourceReliabilityBelief
	leadTimeStates   map[string]*model.SourceLeadTimeBelief
	onChange         func(*model.CompetenceState)
	onPeerChange     func(*model.DeskRelationshipBelief)
	onSourceChange   func(*model.SourceReliabilityBelief)
	onLeadTimeChange func(*model.SourceLeadTimeBelief)
}

func NewGraph() *Graph {
	return &Graph{
		log:            slog.Default().With("component", "belief-graph"),
		states:         make(map[string]*model.CompetenceState),
		peerStates:     make(map[string]*model.DeskRelationshipBelief),
		sourceStates:   make(map[string]*model.SourceReliabilityBelief),
		leadTimeStates: make(map[string]*model.SourceLeadTimeBelief),
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

func SourceBeliefKey(ownerGroup, sourceDomain, signalDomain, language, region string) string {
	return fmt.Sprintf("%s::%s::%s::%s::%s",
		contextOrUnknown(ownerGroup),
		contextOrUnknown(sourceDomain),
		contextOrUnknown(signalDomain),
		contextOrUnknown(language),
		contextOrUnknown(region),
	)
}

func ParseSourceBeliefKey(key string) (ownerGroup, sourceDomain, signalDomain, language, region string) {
	parts := strings.SplitN(key, "::", 5)
	if len(parts) != 5 {
		return "", "", "", "", ""
	}
	return parts[0], parts[1], parts[2], parts[3], parts[4]
}

func LeadTimeBeliefKey(source, signalDomain, language, region string) string {
	return fmt.Sprintf("%s::%s::%s::%s",
		contextOrUnknown(source),
		contextOrUnknown(signalDomain),
		contextOrUnknown(language),
		contextOrUnknown(region),
	)
}

func ParseLeadTimeBeliefKey(key string) (source, signalDomain, language, region string) {
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
	NormalizeCompetenceState(state)
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

func (g *Graph) SetSourceChangeHandler(fn func(*model.SourceReliabilityBelief)) {
	g.mu.Lock()
	g.onSourceChange = fn
	g.mu.Unlock()
}

func (g *Graph) SetLeadTimeChangeHandler(fn func(*model.SourceLeadTimeBelief)) {
	g.mu.Lock()
	g.onLeadTimeChange = fn
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
		NormalizeCompetenceState(state)
		g.states[state.Key] = state
	}
}

func (g *Graph) LoadPeerBeliefs(states []*model.DeskRelationshipBelief) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, incoming := range states {
		if incoming == nil || incoming.Key == "" {
			continue
		}
		state := clonePeerState(incoming)
		if state.OriginDesk == "" || state.ReceivingDesk == "" || state.Domain == "" || state.Regime == "" {
			state.OriginDesk, state.ReceivingDesk, state.Domain, state.Regime = ParsePeerBeliefKey(state.Key)
		}
		normalizePeerState(state)
		g.peerStates[state.Key] = state
	}
}

func (g *Graph) LoadSourceBeliefs(states []*model.SourceReliabilityBelief) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, incoming := range states {
		if incoming == nil || incoming.Key == "" {
			continue
		}
		state := cloneSourceState(incoming)
		if state.OwnerGroup == "" || state.SourceDomain == "" || state.SignalDomain == "" {
			state.OwnerGroup, state.SourceDomain, state.SignalDomain, state.Language, state.Region = ParseSourceBeliefKey(state.Key)
		}
		g.sourceStates[state.Key] = state
	}
}

func (g *Graph) LoadLeadTimeBeliefs(states []*model.SourceLeadTimeBelief) {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, incoming := range states {
		if incoming == nil || incoming.Key == "" {
			continue
		}
		state := cloneLeadTimeState(incoming)
		if state.Source == "" || state.SignalDomain == "" {
			state.Source, state.SignalDomain, state.Language, state.Region = ParseLeadTimeBeliefKey(state.Key)
		}
		g.leadTimeStates[state.Key] = state
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
		Key:                key,
		OriginDesk:         originDesk,
		ReceivingDesk:      receivingDesk,
		Domain:             domain,
		Regime:             regime,
		Trust:              0.55,
		Confidence:         0.35,
		RelationshipHealth: 0.50,
		RecoveryScore:      0.00,
		UpdatedAt:          time.Now(),
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

func (g *Graph) GetSource(key string) *model.SourceReliabilityBelief {
	g.mu.RLock()
	state, exists := g.sourceStates[key]
	g.mu.RUnlock()
	if exists {
		return cloneSourceState(state)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if state, exists = g.sourceStates[key]; exists {
		return cloneSourceState(state)
	}
	ownerGroup, sourceDomain, signalDomain, language, region := ParseSourceBeliefKey(key)
	state = &model.SourceReliabilityBelief{
		Key:          key,
		OwnerGroup:   ownerGroup,
		SourceDomain: sourceDomain,
		SignalDomain: signalDomain,
		Language:     language,
		Region:       region,
		Trust:        0.55,
		Confidence:   0.35,
		UpdatedAt:    time.Now(),
	}
	g.sourceStates[key] = state
	return cloneSourceState(state)
}

func (g *Graph) LookupSource(ownerGroup, sourceDomain, signalDomain, language, region string) (*model.SourceReliabilityBelief, bool) {
	key := SourceBeliefKey(ownerGroup, sourceDomain, signalDomain, language, region)
	g.mu.RLock()
	defer g.mu.RUnlock()
	state, ok := g.sourceStates[key]
	if !ok {
		return nil, false
	}
	return cloneSourceState(state), true
}

func (g *Graph) LookupLeadTime(source, signalDomain, language, region string) (*model.SourceLeadTimeBelief, bool) {
	key := LeadTimeBeliefKey(source, signalDomain, language, region)
	g.mu.RLock()
	defer g.mu.RUnlock()
	state, ok := g.leadTimeStates[key]
	if !ok {
		return nil, false
	}
	return cloneLeadTimeState(state), true
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
	state.SuccessCount++
	state.ValidatedOutcomes++
	state.UpdatedAt = time.Now()
	applyCompetenceCeilings(state)
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
	state.ValidatedOutcomes++
	state.UpdatedAt = time.Now()
	applyCompetenceCeilings(state)
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
	g.ApplyPeerSuccessWithContext(key, magnitude, false, 0)
}

func (g *Graph) ApplyPeerSuccessWithContext(key string, magnitude float64, recovery bool, socialCost float64) {
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
	state.RelationshipHealth += (1 - state.RelationshipHealth) * (0.04 * magnitude)
	if state.RelationshipHealth > 1.0 {
		state.RelationshipHealth = 1.0
	}
	if recovery {
		recoveryWeight := clamp01(socialCost) * 0.08 * magnitude
		state.Trust += (1 - state.Trust) * recoveryWeight
		state.RecoveryScore += (1 - state.RecoveryScore) * recoveryWeight
		if state.RecoveryScore > 1.0 {
			state.RecoveryScore = 1.0
		}
		state.PositiveRecoveries++
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
	g.ApplyPeerFailureWithContext(key, magnitude, 0)
}

func (g *Graph) ApplyPeerFailureWithContext(key string, magnitude float64, faceThreat float64) {
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
	healthWeight := 0.06 * magnitude
	state.RelationshipHealth -= state.RelationshipHealth * healthWeight
	if state.RelationshipHealth < 0 {
		state.RelationshipHealth = 0
	}
	state.RecoveryScore -= state.RecoveryScore * (0.05 * magnitude)
	if state.RecoveryScore < 0 {
		state.RecoveryScore = 0
	}
	if faceThreat >= 0.20 {
		state.NegativeViolations++
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

func (g *Graph) ApplySourceSuccess(key string, magnitude float64) {
	g.GetSource(key)

	g.mu.Lock()
	state := g.sourceStates[key]
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
	changed := cloneSourceState(state)
	handler := g.onSourceChange
	g.mu.Unlock()

	if handler != nil {
		handler(changed)
	}
}

func (g *Graph) ApplySourceFailure(key string, magnitude float64) {
	g.GetSource(key)

	g.mu.Lock()
	state := g.sourceStates[key]
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
	changed := cloneSourceState(state)
	handler := g.onSourceChange
	g.mu.Unlock()

	if handler != nil {
		handler(changed)
	}
}

func (g *Graph) RecordLeadTimeObservation(key string, hours float64) {
	if hours <= 0 {
		return
	}

	g.mu.Lock()
	state, exists := g.leadTimeStates[key]
	if !exists {
		source, signalDomain, language, region := ParseLeadTimeBeliefKey(key)
		state = &model.SourceLeadTimeBelief{
			Key:          key,
			Source:       source,
			SignalDomain: signalDomain,
			Language:     language,
			Region:       region,
		}
		g.leadTimeStates[key] = state
	}
	total := state.AverageHours * float64(state.Observations)
	total += hours
	state.Observations++
	state.AverageHours = total / float64(state.Observations)
	state.Score = leadTimeBeliefScore(state.AverageHours, state.Observations)
	state.UpdatedAt = time.Now()
	changed := cloneLeadTimeState(state)
	handler := g.onLeadTimeChange
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
		applyCompetenceCeilings(state)
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
		stateRegime := state.Regime
		if stateRegime == "" {
			_, _, _, stateRegime = ParseCompetenceKey(key)
		}
		if strings.TrimSpace(regime) != "" && stateRegime != regime {
			continue
		}
		if state.Autonomy == model.Autonomous {
			state.Autonomy = model.Supervised
			g.log.Warn("autonomy dropped due to regime shift",
				"key", key,
				"regime", regime,
				"state_regime", stateRegime,
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

func (g *Graph) AllSourceBeliefs() []*model.SourceReliabilityBelief {
	g.mu.RLock()
	defer g.mu.RUnlock()
	states := make([]*model.SourceReliabilityBelief, 0, len(g.sourceStates))
	for _, s := range g.sourceStates {
		states = append(states, cloneSourceState(s))
	}
	return states
}

func (g *Graph) AllLeadTimeBeliefs() []*model.SourceLeadTimeBelief {
	g.mu.RLock()
	defer g.mu.RUnlock()
	states := make([]*model.SourceLeadTimeBelief, 0, len(g.leadTimeStates))
	for _, s := range g.leadTimeStates {
		states = append(states, cloneLeadTimeState(s))
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

func NormalizeCompetenceState(state *model.CompetenceState) {
	if state == nil {
		return
	}
	if state.ValidatedOutcomes <= 0 {
		state.ValidatedOutcomes = state.TotalObservations()
	}
	applyCompetenceCeilings(state)
}

func applyCompetenceCeilings(state *model.CompetenceState) {
	if state == nil {
		return
	}
	band := ceilingBandForValidatedOutcomes(state.ValidatedOutcomes)
	state.TrustCeiling = band.TrustCeiling
	state.ConfidenceCeiling = band.ConfidenceCeiling
	if state.Trust > state.TrustCeiling {
		state.Trust = state.TrustCeiling
	}
	if state.Confidence > state.ConfidenceCeiling {
		state.Confidence = state.ConfidenceCeiling
	}
	if state.Trust < 0 {
		state.Trust = 0
	}
	if state.Confidence < 0 {
		state.Confidence = 0
	}
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

func normalizePeerState(state *model.DeskRelationshipBelief) {
	if state == nil {
		return
	}
	if state.Trust <= 0 {
		state.Trust = 0.55
	}
	if state.Confidence <= 0 {
		state.Confidence = 0.35
	}
	if state.RelationshipHealth <= 0 {
		state.RelationshipHealth = 0.50
	}
	if state.RecoveryScore < 0 {
		state.RecoveryScore = 0
	}
}

func cloneSourceState(state *model.SourceReliabilityBelief) *model.SourceReliabilityBelief {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func cloneLeadTimeState(state *model.SourceLeadTimeBelief) *model.SourceLeadTimeBelief {
	if state == nil {
		return nil
	}
	cloned := *state
	return &cloned
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func leadTimeBeliefScore(hours float64, observations int) float64 {
	if hours <= 0 || observations <= 0 {
		return 0
	}
	timeScore := hours / 6.0
	if timeScore > 1 {
		timeScore = 1
	}
	confidence := float64(observations) / 5.0
	if confidence > 1 {
		confidence = 1
	}
	return timeScore * confidence
}

func contextOrUnknown(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
