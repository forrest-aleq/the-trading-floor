package model

import "time"

type SourceLeadTimeBelief struct {
	Key          string    `json:"key"`
	Source       string    `json:"source"`
	SignalDomain string    `json:"signal_domain"`
	Language     string    `json:"language"`
	Region       string    `json:"region"`
	AverageHours float64   `json:"average_hours"`
	Observations int       `json:"observations"`
	Score        float64   `json:"score"`
	UpdatedAt    time.Time `json:"updated_at"`
}
