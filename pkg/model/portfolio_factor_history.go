package model

import "time"

type PortfolioFactorHistory struct {
	Factor             string    `json:"factor"`
	Observations       int       `json:"observations"`
	AverageGrossPctNAV float64   `json:"average_gross_pct_nav"`
	MaxGrossPctNAV     float64   `json:"max_gross_pct_nav"`
	AverageDeskCount   float64   `json:"average_desk_count"`
	LastObservedAt     time.Time `json:"last_observed_at"`
}
