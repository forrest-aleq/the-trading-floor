package wire

import (
	"fmt"
	"strings"
	"sync"

	"github.com/hnic/trading-floor/pkg/signal"
)

type signalCluster struct {
	id        string
	typ       signal.Type
	category  string
	centroid  []float32
	texts     []string
	memberIDs []string
}

const clusterLexicalCutoff = 0.39

// Clusterer groups related signals so downstream desks can reason about repeated
// story development instead of isolated feed items.
type Clusterer struct {
	mu                sync.Mutex
	clusters          []*signalCluster
	maxClusters       int
	similarityCutoff  float32
	maxRelatedMembers int
	nextID            int64
}

func NewClusterer(maxClusters int, similarityCutoff float64) *Clusterer {
	return &Clusterer{
		maxClusters:       maxClusters,
		similarityCutoff:  float32(similarityCutoff),
		maxRelatedMembers: 8,
	}
}

func (c *Clusterer) Assign(sig signal.Signal) signal.Signal {
	c.mu.Lock()
	defer c.mu.Unlock()

	bestIdx := -1
	bestScore := float32(-1)
	for i, cluster := range c.clusters {
		if cluster.typ != sig.Type || cluster.category != sig.Category {
			continue
		}

		semanticScore := cosineSimilarity(cluster.centroid, sig.Embedding)
		lexicalScore := textSimilarity(cluster.texts, sig.Translated)
		if semanticScore < c.similarityCutoff && lexicalScore < clusterLexicalCutoff {
			continue
		}

		score := semanticScore
		if lexicalScore > score {
			score = lexicalScore
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		cluster := c.clusters[bestIdx]
		sig.ClusterID = cluster.id
		sig.RelatedSignalIDs = lastN(cluster.memberIDs, c.maxRelatedMembers)
		cluster.memberIDs = append(cluster.memberIDs, sig.ID)
		cluster.centroid = blendCentroid(cluster.centroid, sig.Embedding, len(cluster.memberIDs))
		cluster.texts = appendRecentText(cluster.texts, sig.Translated, 4)
		return sig
	}

	c.nextID++
	clusterID := fmt.Sprintf("cluster-%06d", c.nextID)
	cluster := &signalCluster{
		id:        clusterID,
		typ:       sig.Type,
		category:  sig.Category,
		centroid:  append([]float32(nil), sig.Embedding...),
		texts:     appendRecentText(nil, sig.Translated, 4),
		memberIDs: []string{sig.ID},
	}
	c.clusters = append(c.clusters, cluster)
	if len(c.clusters) > c.maxClusters {
		c.clusters = c.clusters[len(c.clusters)-c.maxClusters:]
	}

	sig.ClusterID = clusterID
	return sig
}

func blendCentroid(current, next []float32, memberCount int) []float32 {
	if len(current) == 0 {
		return append([]float32(nil), next...)
	}

	updated := make([]float32, len(current))
	weightExisting := float32(memberCount-1) / float32(memberCount)
	weightNew := float32(1.0 / float64(memberCount))
	for i := range current {
		var incoming float32
		if i < len(next) {
			incoming = next[i]
		}
		updated[i] = current[i]*weightExisting + incoming*weightNew
	}
	normalizeEmbedding(updated)
	return updated
}

func lastN(items []string, n int) []string {
	if len(items) <= n {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[len(items)-n:]...)
}

func textSimilarity(candidates []string, text string) float32 {
	best := float32(0)
	for _, candidate := range candidates {
		score := shingleDice(candidate, text)
		if tokenScore := tokenDice(candidate, text); tokenScore > score {
			score = tokenScore
		}
		if score > best {
			best = score
		}
	}
	return best
}

func appendRecentText(items []string, text string, maxItems int) []string {
	if text == "" {
		return items
	}

	items = append(items, text)
	if len(items) <= maxItems {
		return items
	}
	return append([]string(nil), items[len(items)-maxItems:]...)
}

func tokenDice(a, b string) float32 {
	left := tokenSet(a)
	right := tokenSet(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}

	intersection := 0
	for token := range left {
		if _, ok := right[token]; ok {
			intersection++
		}
	}
	return float32(2*intersection) / float32(len(left)+len(right))
}

func tokenSet(text string) map[string]struct{} {
	parts := strings.Fields(normalizeForSimilarity(text))
	result := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		result[part] = struct{}{}
	}
	return result
}
