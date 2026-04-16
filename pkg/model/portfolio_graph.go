package model

import "time"

type PortfolioGraphSnapshot struct {
	ID            string                    `json:"id"`
	PortfolioID   string                    `json:"portfolio_id"`
	SessionID     string                    `json:"session_id,omitempty"`
	NAV           float64                   `json:"nav"`
	Cash          float64                   `json:"cash,omitempty"`
	GrossExposure float64                   `json:"gross_exposure,omitempty"`
	NetExposure   float64                   `json:"net_exposure,omitempty"`
	MaxDrawdown   float64                   `json:"max_drawdown,omitempty"`
	OpenPositions int                       `json:"open_positions,omitempty"`
	ObservedAt    time.Time                 `json:"observed_at"`
	Factors       []PortfolioFactorSnapshot `json:"factors,omitempty"`
}

type PortfolioFactorSnapshot struct {
	Factor        string                     `json:"factor"`
	Gross         float64                    `json:"gross"`
	Net           float64                    `json:"net"`
	GrossPctNAV   float64                    `json:"gross_pct_nav"`
	NetPctNAV     float64                    `json:"net_pct_nav"`
	DeskCount     int                        `json:"desk_count"`
	Contributions []FactorContributionRecord `json:"contributions,omitempty"`
}

type FactorContributionRecord struct {
	DeskID     string  `json:"desk_id"`
	Domain     string  `json:"domain,omitempty"`
	Gross      float64 `json:"gross"`
	Net        float64 `json:"net"`
	GrossShare float64 `json:"gross_share,omitempty"`
}
