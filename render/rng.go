package render

// rng is a splitmix64 stream. Sampling reproducibility comes from
// seeding one stream per (master seed, pixel, round) triple, so the
// random numbers a pixel consumes never depend on thread count,
// scheduling, or which round is rendered first.
type rng struct{ s uint64 }

func newRNG(seed uint64, pixel, round int) *rng {
	r := &rng{s: seed ^ (uint64(pixel)+1)*0x9E3779B97F4A7C15 ^ (uint64(round)+1)*0xBF58476D1CE4E5B9}
	r.next() // warm up: mix the seed once
	return r
}

func (r *rng) next() uint64 {
	r.s += 0x9E3779B97F4A7C15
	z := r.s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// float returns a uniform value in [0, 1).
func (r *rng) float() float64 {
	return float64(r.next()>>11) * (1.0 / 9007199254740992.0)
}
