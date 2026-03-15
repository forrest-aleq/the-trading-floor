package wire

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

const embeddingDimensions = 1536

// EmbedText builds a lightweight deterministic embedding for dedup/clustering.
// It is not model-quality, but it gives the wire semantic memory instead of
// exact-hash-only behavior.
func EmbedText(text string) []float32 {
	vec := make([]float32, embeddingDimensions)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return vec
	}

	for _, token := range tokens {
		idxHash := fnv.New32a()
		_, _ = idxHash.Write([]byte(token))
		idx := idxHash.Sum32() % embeddingDimensions

		signHash := fnv.New32()
		_, _ = signHash.Write([]byte(token))
		sign := float32(1.0)
		if signHash.Sum32()%2 == 1 {
			sign = -1.0
		}

		vec[idx] += sign
	}

	normalizeEmbedding(vec)
	return vec
}

func tokenize(text string) []string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r):
			return ' '
		default:
			return ' '
		}
	}, text)

	raw := strings.Fields(cleaned)
	tokens := make([]string, 0, len(raw))
	for _, token := range raw {
		if len(token) < 2 {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func normalizeEmbedding(vec []float32) {
	sumSquares := 0.0
	for _, value := range vec {
		sumSquares += float64(value * value)
	}
	if sumSquares == 0 {
		return
	}

	norm := float32(math.Sqrt(sumSquares))
	for i, value := range vec {
		vec[i] = value / norm
	}
}
