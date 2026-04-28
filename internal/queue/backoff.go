package queue

import (
	"math/rand/v2"
	"time"
)

const (
	backoffBase      = 2 * time.Second
	backoffMax       = 5 * time.Minute
	backoffJitterPct = 0.25 // ±25%
)

// nextBackoff returns the duration to wait before the (attempt+1)th
// retry of a job. The base grows exponentially (2s, 4s, 8s, …),
// capped at 5 minutes. ±25% jitter spreads retries when many jobs
// fail simultaneously.
//
// attempt counts from 1 (the very first retry waits ~2s); attempt=0
// is treated as 1 to avoid pathological zero-wait loops.
func nextBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := backoffBase
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= backoffMax {
			d = backoffMax
			break
		}
	}
	// Apply jitter: multiply by a factor in [1, 1+jitter], then cap.
	// Using only positive jitter ensures the output never falls below the
	// pre-jitter value, keeping capped attempts firmly within [base, backoffMax].
	factor := 1 + rand.Float64()*backoffJitterPct
	out := time.Duration(float64(d) * factor)
	if out > backoffMax {
		out = backoffMax
	}
	return out
}
