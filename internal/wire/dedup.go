package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"unicode"

	"github.com/hnic/trading-floor/pkg/signal"
)

type dedupEntry struct {
	hash      string
	source    string
	typ       signal.Type
	category  string
	text      string
	embedding []float32
}

// Deduper tracks both exact and near-duplicate signals.
type Deduper struct {
	mu                sync.Mutex
	seenHashes        map[string]struct{}
	recent            []dedupEntry
	maxRecent         int
	semanticThreshold float32
}

func NewDeduper(maxRecent int, semanticThreshold float64) *Deduper {
	return &Deduper{
		seenHashes:        make(map[string]struct{}),
		maxRecent:         maxRecent,
		semanticThreshold: float32(semanticThreshold),
	}
}

func (d *Deduper) IsDuplicate(sig signal.Signal) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.seenHashes[sig.ContentHash]; ok {
		return true
	}
	if exactHashOnlySource(sig.Source) {
		d.seenHashes[sig.ContentHash] = struct{}{}
		return false
	}

	for _, entry := range d.recent {
		if exactHashOnlySource(entry.source) {
			continue
		}
		if entry.typ != sig.Type || entry.category != sig.Category {
			continue
		}
		if cosineSimilarity(entry.embedding, sig.Embedding) >= d.semanticThreshold ||
			shingleDice(entry.text, sig.Translated) >= 0.45 {
			d.seenHashes[sig.ContentHash] = struct{}{}
			return true
		}
	}

	d.seenHashes[sig.ContentHash] = struct{}{}
	d.recent = append(d.recent, dedupEntry{
		hash:      sig.ContentHash,
		source:    sig.Source,
		typ:       sig.Type,
		category:  sig.Category,
		text:      sig.Translated,
		embedding: append([]float32(nil), sig.Embedding...),
	})
	if len(d.recent) > d.maxRecent {
		d.recent = d.recent[len(d.recent)-d.maxRecent:]
	}
	return false
}

func exactHashOnlySource(source string) bool {
	return strings.EqualFold(strings.TrimSpace(source), "kalshi-market")
}

func hashSignalContent(sig signal.Signal) string {
	h := sha256.New()
	h.Write([]byte(sig.Source))
	h.Write([]byte(string(sig.Type)))
	h.Write([]byte(sig.Category))
	h.Write([]byte(canonicalText(sig)))
	return hex.EncodeToString(h.Sum(nil))
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}

	var dot float32
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
	}
	return dot
}

func shingleDice(a, b string) float32 {
	left := shingles(normalizeForSimilarity(a), 4)
	right := shingles(normalizeForSimilarity(b), 4)
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

func normalizeForSimilarity(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r):
			return ' '
		default:
			return ' '
		}
	}, text)
}

func shingles(text string, width int) map[string]struct{} {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) < width {
		return map[string]struct{}{text: {}}
	}

	result := make(map[string]struct{}, len(text)-width+1)
	for i := 0; i <= len(text)-width; i++ {
		result[text[i:i+width]] = struct{}{}
	}
	return result
}
