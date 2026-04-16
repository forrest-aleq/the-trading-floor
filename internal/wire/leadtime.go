package wire

import (
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/evidence"
	"github.com/hnic/trading-floor/pkg/signal"
)

type leadRef struct {
	id         string
	source     string
	category   string
	ownerGroup string
	language   string
	region     string
	timestamp  time.Time
}

type leadNarrative struct {
	id    string
	refs  map[string]leadRef
	order []string
}

type leadStat struct {
	totalHours   float64
	observations int
	lastUpdated  time.Time
}

func (s leadStat) averageHours() float64 {
	if s.observations == 0 {
		return 0
	}
	return s.totalHours / float64(s.observations)
}

type LeadTimeTracker struct {
	mu                  sync.Mutex
	narratives          map[string]*leadNarrative
	narrativeOrder      []string
	stats               map[string]leadStat
	maxNarratives       int
	maxRefsPerNarrative int
	onObservation       func(source, category, language, region string, observedHours float64)
}

func NewLeadTimeTracker(maxNarratives, maxRefsPerNarrative int) *LeadTimeTracker {
	if maxNarratives <= 0 {
		maxNarratives = 2048
	}
	if maxRefsPerNarrative <= 0 {
		maxRefsPerNarrative = 16
	}
	return &LeadTimeTracker{
		narratives:          make(map[string]*leadNarrative),
		stats:               make(map[string]leadStat),
		maxNarratives:       maxNarratives,
		maxRefsPerNarrative: maxRefsPerNarrative,
	}
}

func (t *LeadTimeTracker) SetObservationHandler(fn func(source, category, language, region string, observedHours float64)) {
	t.mu.Lock()
	t.onObservation = fn
	t.mu.Unlock()
}

func (t *LeadTimeTracker) Enrich(sig signal.Signal) signal.Signal {
	t.mu.Lock()
	defer t.mu.Unlock()

	if sig.EvidenceMeta == nil {
		sig.EvidenceMeta = buildEvidenceMeta(sig)
	}
	meta := sig.EvidenceMeta.Clone()
	if meta == nil {
		meta = &evidence.Metadata{}
	}

	statsKey := leadStatKey(sig.Source, sig.Category, signalLanguage(sig), meta.OriginRegion)
	if stats, ok := t.stats[statsKey]; ok {
		meta.LeadTimeAverageHours = roundEvidence(stats.averageHours())
		meta.LeadTimeObservations = stats.observations
		meta.LeadTimeScore = roundEvidence(leadTimeScore(stats.averageHours(), stats.observations))
	}

	narrativeID := strings.TrimSpace(sig.NarrativeClusterID)
	if narrativeID == "" {
		sig.EvidenceMeta = refreshEvidenceAssessment(sig, meta)
		return sig
	}

	narrative := t.ensureNarrative(narrativeID)
	timestamp := sig.Timestamp.UTC()
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	if isConsensusSignal(sig, meta) {
		for _, refID := range narrative.order {
			ref := narrative.refs[refID]
			if ref.id == sig.ID || !ref.timestamp.Before(timestamp) {
				continue
			}
			if ref.ownerGroup != "" && ref.ownerGroup == meta.SourceOwnerGroup {
				continue
			}
			if normalizeLanguageCode(ref.language) == "en" && normalizeRegionLabel(ref.region) == "global" {
				continue
			}
			hours := timestamp.Sub(ref.timestamp).Hours()
			if hours <= 0 || hours > 168 {
				continue
			}
			key := leadStatKey(ref.source, ref.category, ref.language, ref.region)
			stats := t.stats[key]
			stats.totalHours += hours
			stats.observations++
			stats.lastUpdated = timestamp
			t.stats[key] = stats
			if t.onObservation != nil {
				t.onObservation(ref.source, ref.category, ref.language, ref.region, hours)
			}
		}
	}

	if _, exists := narrative.refs[sig.ID]; !exists {
		narrative.refs[sig.ID] = leadRef{
			id:         sig.ID,
			source:     strings.TrimSpace(sig.Source),
			category:   strings.TrimSpace(sig.Category),
			ownerGroup: meta.SourceOwnerGroup,
			language:   signalLanguage(sig),
			region:     meta.OriginRegion,
			timestamp:  timestamp,
		}
		narrative.order = append(narrative.order, sig.ID)
		if len(narrative.order) > t.maxRefsPerNarrative {
			oldest := narrative.order[0]
			narrative.order = narrative.order[1:]
			delete(narrative.refs, oldest)
		}
	}

	sig.EvidenceMeta = refreshEvidenceAssessment(sig, meta)
	return sig
}

func (t *LeadTimeTracker) ensureNarrative(id string) *leadNarrative {
	if narrative, ok := t.narratives[id]; ok {
		return narrative
	}
	narrative := &leadNarrative{
		id:   id,
		refs: make(map[string]leadRef),
	}
	t.narratives[id] = narrative
	t.narrativeOrder = append(t.narrativeOrder, id)
	if len(t.narrativeOrder) > t.maxNarratives {
		evict := t.narrativeOrder[0]
		t.narrativeOrder = t.narrativeOrder[1:]
		delete(t.narratives, evict)
	}
	return narrative
}

func isConsensusSignal(sig signal.Signal, meta *evidence.Metadata) bool {
	if meta == nil || normalizeLanguageCode(signalLanguage(sig)) != "en" {
		return false
	}
	if meta.SourceTier == "primary" || meta.SourceTier == "major_press" || meta.SourceType == "market" {
		return true
	}
	return meta.SourceTrust >= 0.84
}

func leadStatKey(source, category, language, region string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	category = strings.TrimSpace(strings.ToLower(category))
	language = normalizeLanguageCode(language)
	if language == "" {
		language = "unknown"
	}
	region = normalizeRegionLabel(region)
	if region == "" {
		region = "global"
	}
	return source + "|" + category + "|" + language + "|" + region
}

func leadTimeScore(hours float64, observations int) float64 {
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
