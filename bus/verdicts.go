package bus

// RollUp computes the 4-eyes state of a subject from its verdicts, which MUST be
// ordered oldest→newest (as Bus.Verdicts returns them). An approve counts only
// when reviewer != author. A reject supersedes earlier verdicts, but a later
// independent approve wins again. Returns the state and the index of the
// deciding entry (-1 for PENDING).
func RollUp(vs []Verdict) (state string, deciderIdx int) {
	indepApproveIdx, lastRejectIdx := -1, -1
	for i := range vs {
		switch vs[i].Decision {
		case "approve":
			if vs[i].Reviewer != vs[i].Author {
				indepApproveIdx = i
			}
		case "reject":
			lastRejectIdx = i
		}
	}
	switch {
	case indepApproveIdx >= 0 && indepApproveIdx > lastRejectIdx:
		return "APPROVED", indepApproveIdx
	case lastRejectIdx >= 0:
		return "REJECTED", lastRejectIdx
	default:
		return "PENDING", -1
	}
}
