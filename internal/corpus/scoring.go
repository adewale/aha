package corpus

import "math"

// spread weights how widely a cluster/path recurs across sessions and projects:
// 1 + (sessions-1) + 0.5*(projects-1), clamped to >= 0 for degenerate inputs.
// Normal inputs have sessions>=1, projects>=1, giving spread>=1.
func spread(sessions, projects int) float64 {
	s := 1.0 + float64(sessions-1) + 0.5*float64(projects-1)
	if s < 0 {
		return 0
	}
	return s
}

// wilsonLowerBound returns the lower bound of the Wilson score interval for a
// binomial proportion of `successes` out of `trials`, at z-score `z`
// (use z=1.96 for ~95%). Returns 0 when trials <= 0. Requires 0 <= successes <= trials.
func wilsonLowerBound(successes, trials int, z float64) float64 {
	if trials <= 0 {
		return 0
	}
	n := float64(trials)
	phat := float64(successes) / n
	z2 := z * z
	bound := (phat + z2/(2*n) - z*math.Sqrt((phat*(1-phat)+z2/(4*n))/n)) / (1 + z2/n)
	if bound < 0 {
		return 0
	}
	if bound > 1 {
		return 1
	}
	return bound
}

// pathScore ranks a resolution path: its Wilson-bound confidence times spread.
func pathScore(confidence float64, sessions, projects int) float64 {
	return confidence * spread(sessions, projects)
}
