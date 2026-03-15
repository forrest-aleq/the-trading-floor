package signal

func (s Signal) EvidenceGate() (bool, string) {
	if s.EvidenceMeta == nil {
		return true, ""
	}
	return s.EvidenceMeta.DeterministicGate()
}
