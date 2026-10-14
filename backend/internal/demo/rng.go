// Package demo provides a deterministic, in-memory implementation of the API
// service contracts. It exists so the frontend can run against the real REST
// surface in Phase 1 while Postgres repositories are built. All data is
// synthetic, seeded, and clearly labeled — nothing here is live market data.
package demo

import "math"

// hashSeed is FNV-1a over the key — stable across runs and processes.
func hashSeed(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// rng is a mulberry32 PRNG producing float64 in [0, 1).
type rng struct{ a uint32 }

func newRng(key string) *rng { return &rng{a: hashSeed(key)} }

func (r *rng) next() float64 {
	r.a += 0x6d2b79f5
	t := r.a
	t = (t ^ (t >> 15)) * (1 | t)
	t = (t + (t^(t>>7))*(61|t)) ^ t
	return float64((t ^ (t >> 14))) / 4294967296
}

func (r *rng) between(min, max float64) float64 { return min + r.next()*(max-min) }

func (r *rng) intBetween(min, max int) int {
	return min + int(r.next()*float64(max-min+1))
}

func (r *rng) pick(items []string) string { return items[r.intBetween(0, len(items)-1)] }

func round(v float64, digits int) float64 {
	f := math.Pow(10, float64(digits))
	return math.Round(v*f) / f
}
