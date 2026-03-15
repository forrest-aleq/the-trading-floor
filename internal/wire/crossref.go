package wire

import (
	"strings"
	"sync"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/signal"
)

type signalRef struct {
	id         string
	source     string
	cluster    string
	entities   []string
	text       string
	ownerGroup string
	trust      float64
	primary    bool
}

// CrossReferencer links repeated evidence across clusters and entities so desks
// receive corroborated context instead of isolated single-feed payloads.
type CrossReferencer struct {
	mu sync.Mutex

	byCluster map[string][]signalRef
	byEntity  map[string][]signalRef
	history   []signalRef

	maxHistory int
	maxPerKey  int
	maxRelated int
	maxLabels  int
}

func NewCrossReferencer(maxHistory, maxPerKey int) *CrossReferencer {
	if maxHistory <= 0 {
		maxHistory = 4096
	}
	if maxPerKey <= 0 {
		maxPerKey = 16
	}

	return &CrossReferencer{
		byCluster:  make(map[string][]signalRef),
		byEntity:   make(map[string][]signalRef),
		maxHistory: maxHistory,
		maxPerKey:  maxPerKey,
		maxRelated: 12,
		maxLabels:  8,
	}
}

func (c *CrossReferencer) Enrich(sig signal.Signal) signal.Signal {
	c.mu.Lock()
	defer c.mu.Unlock()

	if sig.EvidenceMeta == nil {
		sig.EvidenceMeta = buildEvidenceMeta(sig)
	}
	meta := sig.EvidenceMeta.Clone()
	if meta == nil {
		meta = &evidence.Metadata{}
	}

	related := newOrderedStrings(sig.RelatedSignalIDs...)
	sources := newOrderedStrings()
	entities := newOrderedStrings()
	ownerGroups := newOrderedStrings()
	if meta.SourceOwnerGroup != "" {
		ownerGroups.Add(meta.SourceOwnerGroup)
	}
	corroboratingOwnerGroups := newOrderedStrings()
	candidateRefs := make(map[string]signalRef)

	if sig.ClusterID != "" {
		for _, ref := range c.byCluster[sig.ClusterID] {
			candidateRefs[ref.id] = ref
			if ref.id != sig.ID {
				related.Add(ref.id)
			}
			if ref.source != "" && ref.source != sig.Source {
				sources.Add(ref.source)
			}
			if ref.ownerGroup != "" {
				ownerGroups.Add(ref.ownerGroup)
				if ref.ownerGroup != meta.SourceOwnerGroup {
					corroboratingOwnerGroups.Add(ref.ownerGroup)
				}
			}
			if ref.primary {
				meta.HasPrimarySource = true
			}
		}
	}

	displayNames := entityDisplayNames(sig.Entities)
	for _, key := range entityKeys(sig.Entities) {
		matched := false
		for _, ref := range c.byEntity[key] {
			candidateRefs[ref.id] = ref
			if ref.id != sig.ID {
				related.Add(ref.id)
			}
			if ref.source != "" && ref.source != sig.Source {
				sources.Add(ref.source)
			}
			if ref.ownerGroup != "" {
				ownerGroups.Add(ref.ownerGroup)
				if ref.ownerGroup != meta.SourceOwnerGroup {
					corroboratingOwnerGroups.Add(ref.ownerGroup)
				}
			}
			if ref.primary {
				meta.HasPrimarySource = true
			}
			matched = true
		}
		if matched {
			if name, ok := displayNames[key]; ok {
				entities.Add(name)
			}
		}
	}

	sig.RelatedSignalIDs = related.Last(c.maxRelated)
	sig.CorroboratingSources = sources.Last(c.maxLabels)
	sig.CorroboratingEntities = entities.Last(c.maxLabels)
	meta.CorroboratingOwnerGroups = corroboratingOwnerGroups.Last(c.maxLabels)
	meta.DistinctSources = len(newOrderedStrings(append([]string{sig.Source}, sig.CorroboratingSources...)...).items)
	meta.DistinctOwnerGroups = len(ownerGroups.items)
	meta.HasPrimarySource = meta.HasPrimarySource || meta.SourceTier == "primary" || meta.SourceType == "primary" || meta.SourceType == "market"

	refs := make([]signalRef, 0, len(candidateRefs))
	for _, ref := range candidateRefs {
		refs = append(refs, ref)
	}
	meta.ContradictionCount, meta.ContradictionSeverity, meta.ConflictingSignalIDs = detectContradictions(sig, refs)
	meta.EvidenceScore = roundEvidence(scoreEvidence(meta))
	sig.EvidenceMeta = meta

	c.record(sig)
	return sig
}

func (c *CrossReferencer) record(sig signal.Signal) {
	ref := signalRef{
		id:       sig.ID,
		source:   sig.Source,
		cluster:  sig.ClusterID,
		entities: entityKeys(sig.Entities),
		text:     canonicalText(sig),
	}
	if sig.EvidenceMeta != nil {
		ref.ownerGroup = sig.EvidenceMeta.SourceOwnerGroup
		ref.trust = sig.EvidenceMeta.SourceTrust
		ref.primary = sig.EvidenceMeta.SourceTier == "primary" || sig.EvidenceMeta.SourceType == "primary" || sig.EvidenceMeta.SourceType == "market"
	}

	if ref.cluster != "" {
		c.byCluster[ref.cluster] = appendAndTrimRefs(c.byCluster[ref.cluster], ref, c.maxPerKey)
	}
	for _, entity := range ref.entities {
		c.byEntity[entity] = appendAndTrimRefs(c.byEntity[entity], ref, c.maxPerKey)
	}

	c.history = append(c.history, ref)
	if len(c.history) <= c.maxHistory {
		return
	}

	evicted := c.history[0]
	c.history = append([]signalRef(nil), c.history[1:]...)
	c.removeRef(evicted)
}

func (c *CrossReferencer) removeRef(ref signalRef) {
	if ref.cluster != "" {
		c.byCluster[ref.cluster] = removeSignalRef(c.byCluster[ref.cluster], ref.id)
		if len(c.byCluster[ref.cluster]) == 0 {
			delete(c.byCluster, ref.cluster)
		}
	}
	for _, entity := range ref.entities {
		c.byEntity[entity] = removeSignalRef(c.byEntity[entity], ref.id)
		if len(c.byEntity[entity]) == 0 {
			delete(c.byEntity, entity)
		}
	}
}

func appendAndTrimRefs(refs []signalRef, ref signalRef, max int) []signalRef {
	refs = append(refs, ref)
	if len(refs) <= max {
		return refs
	}
	return append([]signalRef(nil), refs[len(refs)-max:]...)
}

func removeSignalRef(refs []signalRef, id string) []signalRef {
	for i, ref := range refs {
		if ref.id == id {
			return append(refs[:i], refs[i+1:]...)
		}
	}
	return refs
}

func entityKeys(entities []signal.Entity) []string {
	seen := make(map[string]struct{}, len(entities))
	keys := make([]string, 0, len(entities))
	for _, entity := range entities {
		key := normalizeEntityKey(entity.Name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}

func entityDisplayNames(entities []signal.Entity) map[string]string {
	names := make(map[string]string, len(entities))
	for _, entity := range entities {
		key := normalizeEntityKey(entity.Name)
		if key == "" {
			continue
		}
		if _, ok := names[key]; ok {
			continue
		}
		names[key] = strings.TrimSpace(entity.Name)
	}
	return names
}

func normalizeEntityKey(name string) string {
	name = strings.TrimSpace(strings.ToUpper(name))
	if name == "" {
		return ""
	}
	return strings.Join(strings.Fields(name), " ")
}

type orderedStrings struct {
	items []string
	seen  map[string]struct{}
}

func newOrderedStrings(seed ...string) orderedStrings {
	set := orderedStrings{
		items: make([]string, 0, len(seed)),
		seen:  make(map[string]struct{}, len(seed)),
	}
	for _, value := range seed {
		set.Add(value)
	}
	return set
}

func (s *orderedStrings) Add(value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := s.seen[value]; ok {
		return
	}
	s.seen[value] = struct{}{}
	s.items = append(s.items, value)
}

func (s orderedStrings) Last(max int) []string {
	if max <= 0 || len(s.items) <= max {
		return append([]string(nil), s.items...)
	}
	return append([]string(nil), s.items[len(s.items)-max:]...)
}
