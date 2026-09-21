package stats

// Rating2Approx is the community approximation of HLTV Rating 2.0 (label it "Rating 2.0 (approx.)").
//
//	Impact = 2.13·KPR + 0.42·APR − 0.41
//	Rating = 0.0073·KAST% + 0.3591·KPR − 0.5329·DPR + 0.2372·Impact + 0.0032·ADR + 0.1587
//
// kast is a 0..1 fraction; the formula expects percentage points.
func Rating2Approx(k, d, a, rounds int, kast, adr float64) float64 {
	if rounds == 0 {
		return 0
	}
	r := float64(rounds)
	kpr, dpr, apr := float64(k)/r, float64(d)/r, float64(a)/r
	impact := 2.13*kpr + 0.42*apr - 0.41
	return 0.0073*kast*100 + 0.3591*kpr - 0.5329*dpr + 0.2372*impact + 0.0032*adr + 0.1587
}
