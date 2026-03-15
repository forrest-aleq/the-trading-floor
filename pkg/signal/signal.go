package signal

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/hnic/trading-floor/pkg/evidence"
)

type Type string

const (
	TypeNews        Type = "news"
	TypePrice       Type = "price"
	TypeEconomic    Type = "economic"
	TypeFiling      Type = "filing"
	TypeSocial      Type = "social"
	TypeAlternative Type = "alternative"
	TypeFlow        Type = "flow"
)

type Direction string

const (
	Bullish Direction = "bullish"
	Bearish Direction = "bearish"
	Neutral Direction = "neutral"
)

type Signal struct {
	ID                    string             `json:"id"`
	Source                string             `json:"source"`
	Type                  Type               `json:"type"`
	Category              string             `json:"category"`
	Timestamp             time.Time          `json:"timestamp"`
	Urgency               float64            `json:"urgency"`
	Strength              float64            `json:"strength"`
	Direction             Direction          `json:"direction,omitempty"`
	Entities              []Entity           `json:"entities,omitempty"`
	Languages             []string           `json:"languages,omitempty"`
	Raw                   json.RawMessage    `json:"raw,omitempty"`
	Translated            string             `json:"translated,omitempty"`
	Embedding             []float32          `json:"embedding,omitempty"`
	ContentHash           string             `json:"content_hash,omitempty"`
	ClusterID             string             `json:"cluster_id,omitempty"`
	RelatedSignalIDs      []string           `json:"related_signal_ids,omitempty"`
	CorroboratingSources  []string           `json:"corroborating_sources,omitempty"`
	CorroboratingEntities []string           `json:"corroborating_entities,omitempty"`
	EvidenceMeta          *evidence.Metadata `json:"evidence_meta,omitempty"`
}

type Entity struct {
	Name string `json:"name"`
	Type string `json:"type"` // company, person, instrument, country, sector
	ID   string `json:"id,omitempty"`
}

func New(source string, typ Type, content string) Signal {
	return Signal{
		ID:        uuid.New().String(),
		Source:    source,
		Type:      typ,
		Timestamp: time.Now(),
		Raw:       json.RawMessage(`"` + content + `"`),
	}
}
