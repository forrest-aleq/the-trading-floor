package model

type ExpectationState struct {
	Domain               string   `json:"domain,omitempty"`
	PredictedImportance  float64  `json:"predicted_importance,omitempty"`
	PredictedReliability float64  `json:"predicted_reliability,omitempty"`
	PredictedTradability float64  `json:"predicted_tradability,omitempty"`
	PredictedNovelty     float64  `json:"predicted_novelty,omitempty"`
	PredictedDirection   string   `json:"predicted_direction,omitempty"`
	PredictedAction      string   `json:"predicted_action,omitempty"`
	Basis                []string `json:"basis,omitempty"`
}

type AppraisalState struct {
	Domain              string   `json:"domain,omitempty"`
	ObservedReliability float64  `json:"observed_reliability,omitempty"`
	ExpectationGap      float64  `json:"expectation_gap,omitempty"`
	ViolationScore      float64  `json:"violation_score,omitempty"`
	ViolationClass      string   `json:"violation_class,omitempty"`
	Power               float64  `json:"power,omitempty"`
	Distance            float64  `json:"distance,omitempty"`
	Rank                float64  `json:"rank,omitempty"`
	FaceThreatScore     float64  `json:"face_threat_score,omitempty"`
	SocialCost          float64  `json:"social_cost,omitempty"`
	ActionPressure      float64  `json:"action_pressure,omitempty"`
	RelationshipHealth  float64  `json:"relationship_health,omitempty"`
	Basis               []string `json:"basis,omitempty"`
}
