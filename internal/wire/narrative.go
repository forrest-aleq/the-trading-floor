package wire

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hnic/trading-floor/pkg/signal"
)

type narrativeCluster struct {
	id        string
	category  string
	texts     []string
	memberIDs []string
	entities  map[string]struct{}
	languages map[string]struct{}
	firstSeen time.Time
	lastSeen  time.Time
}

type NarrativeCorrelator struct {
	mu           sync.Mutex
	clusters     []*narrativeCluster
	maxClusters  int
	textCutoff   float32
	entityCutoff float32
	nextID       int64
}

func NewNarrativeCorrelator(maxClusters int) *NarrativeCorrelator {
	if maxClusters <= 0 {
		maxClusters = 1024
	}
	return &NarrativeCorrelator{
		maxClusters:  maxClusters,
		textCutoff:   0.46,
		entityCutoff: 0.34,
	}
}

func (c *NarrativeCorrelator) Assign(sig signal.Signal) signal.Signal {
	c.mu.Lock()
	defer c.mu.Unlock()

	text := narrativeText(sig)
	if text == "" {
		return sig
	}
	entityKeys := entityKeys(sig.Entities, signalLanguage(sig))

	bestIdx := -1
	bestScore := float32(0)
	for i, cluster := range c.clusters {
		if cluster.category != sig.Category {
			continue
		}
		textScore := textSimilarity(cluster.texts, text)
		entityScore := entityOverlap(cluster.entities, entityKeys)
		if textScore < c.textCutoff && entityScore < c.entityCutoff {
			continue
		}
		score := textScore
		if entityScore > score {
			score = entityScore
		}
		if textScore >= c.textCutoff && entityScore > 0 {
			score += 0.12
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		cluster := c.clusters[bestIdx]
		sig.NarrativeClusterID = cluster.id
		cluster.memberIDs = append(cluster.memberIDs, sig.ID)
		cluster.texts = appendRecentText(cluster.texts, text, 6)
		if cluster.entities == nil {
			cluster.entities = make(map[string]struct{}, len(entityKeys))
		}
		for _, key := range entityKeys {
			cluster.entities[key] = struct{}{}
		}
		if cluster.languages == nil {
			cluster.languages = make(map[string]struct{}, 2)
		}
		cluster.languages[signalLanguage(sig)] = struct{}{}
		if cluster.firstSeen.IsZero() || (!sig.Timestamp.IsZero() && sig.Timestamp.Before(cluster.firstSeen)) {
			cluster.firstSeen = sig.Timestamp
		}
		if sig.Timestamp.After(cluster.lastSeen) {
			cluster.lastSeen = sig.Timestamp
		}
		return sig
	}

	c.nextID++
	clusterID := fmt.Sprintf("narrative-%06d", c.nextID)
	entities := make(map[string]struct{}, len(entityKeys))
	for _, key := range entityKeys {
		entities[key] = struct{}{}
	}
	c.clusters = append(c.clusters, &narrativeCluster{
		id:        clusterID,
		category:  sig.Category,
		texts:     appendRecentText(nil, text, 6),
		memberIDs: []string{sig.ID},
		entities:  entities,
		languages: map[string]struct{}{signalLanguage(sig): {}},
		firstSeen: sig.Timestamp,
		lastSeen:  sig.Timestamp,
	})
	if len(c.clusters) > c.maxClusters {
		c.clusters = c.clusters[len(c.clusters)-c.maxClusters:]
	}
	sig.NarrativeClusterID = clusterID
	return sig
}

func narrativeText(sig signal.Signal) string {
	text := strings.TrimSpace(sig.Translated)
	if text == "" {
		text = strings.TrimSpace(sig.OriginalText)
	}
	if strings.HasPrefix(strings.ToLower(text), "telegram ") {
		if idx := strings.Index(text, ":"); idx > 0 && idx+1 < len(text) {
			text = strings.TrimSpace(text[idx+1:])
		}
	}
	return text
}

func entityOverlap(existing map[string]struct{}, incoming []string) float32 {
	if len(existing) == 0 || len(incoming) == 0 {
		return 0
	}
	intersection := 0
	for _, key := range incoming {
		if _, ok := existing[key]; ok {
			intersection++
		}
	}
	return float32(intersection) / float32(len(incoming))
}
