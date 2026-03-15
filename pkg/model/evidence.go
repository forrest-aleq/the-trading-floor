package model

func (o Opportunity) EvidenceGate() (bool, string) {
	if o.EvidenceMeta == nil {
		return true, ""
	}
	return o.EvidenceMeta.DeterministicGate()
}

func (t Thesis) EvidenceRiskGate() (bool, string) {
	if t.EvidenceMeta == nil {
		return true, ""
	}
	return t.EvidenceMeta.DeterministicGate()
}
