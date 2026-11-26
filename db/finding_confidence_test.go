package db

import "testing"

func TestFindingConfidence(t *testing.T) {
	cases := []struct {
		name string
		sig  ConfidenceSignal
		want int
	}{
		{"maxed", ConfidenceSignal{PoELevel: 4, VerificationStatus: VerificationVerified, OASTTag: "tag",
			ProofBindings: 3, DistinctWorkers: 3, HasCounterevidence: true, HasRationale: true}, 100},
		{"bare unverified", ConfidenceSignal{VerificationStatus: VerificationUnverified}, 0},
		{"proof only", ConfidenceSignal{VerificationStatus: VerificationUnverified, ProofBindings: 1, HasRationale: true}, 11},
		{"false positive clamps", ConfidenceSignal{VerificationStatus: VerificationFalsePositive}, 0},
		{"blocked", ConfidenceSignal{VerificationStatus: VerificationBlocked, PoELevel: 3, HasCounterevidence: true, HasRationale: true}, 36},
		{"not assessed", ConfidenceSignal{VerificationStatus: VerificationNotAssessed, HasCounterevidence: true, HasRationale: true}, 5},
		{"single worker no bonus", ConfidenceSignal{VerificationStatus: VerificationVerified, DistinctWorkers: 1, HasCounterevidence: true, HasRationale: true}, 35},
	}
	for _, c := range cases {
		if got := FindingConfidence(c.sig); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
