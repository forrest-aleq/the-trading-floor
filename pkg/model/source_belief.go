package model

import "time"

type SourceReliabilityBelief struct {
	Key          string    `json:"key"`
	SourceDomain string    `json:"source_domain"`
	OwnerGroup   string    `json:"owner_group"`
	SignalDomain string    `json:"signal_domain"`
	Language     string    `json:"language"`
	Region       string    `json:"region"`
	Trust        float64   `json:"trust"`
	Confidence   float64   `json:"confidence"`
	SuccessCount int       `json:"success_count"`
	FailureCount int       `json:"failure_count"`
	UpdatedAt    time.Time `json:"updated_at"`
}
