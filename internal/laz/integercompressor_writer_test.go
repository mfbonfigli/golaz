// Copyright (c) 2026 Massimo Federico Bonfigli
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package laz

import (
	"math"
	"math/rand"
	"testing"
)

// icPair is one (prediction, real value) compression input.
type icPair struct {
	pred, real int32
}

// icRoundTrip compresses the pairs with a fresh compressor, then decompresses
// with the existing IntegerDecompressor and asserts value and GetK() parity
// for every pair. Contexts cycle over the pair index.
func icRoundTrip(t *testing.T, bits, contexts, bitsHigh, range_ uint32, pairs []icPair) {
	t.Helper()

	// Compress.
	out := NewByteStreamOutArray()
	enc := NewArithmeticEncoder()
	if err := enc.Init(out); err != nil {
		t.Fatalf("enc.Init: %v", err)
	}
	icEnc := NewIntegerCompressor(enc, bits, contexts, bitsHigh, range_)
	icEnc.InitCompressor()
	encKs := make([]uint32, len(pairs))
	for i, p := range pairs {
		if err := icEnc.Compress(p.pred, p.real, uint32(i)%contexts); err != nil {
			t.Fatalf("pair %d: Compress: %v", i, err)
		}
		encKs[i] = icEnc.GetK()
	}
	if _, err := enc.Done(); err != nil {
		t.Fatalf("enc.Done: %v", err)
	}

	// Decompress.
	dec := NewArithmeticDecoder()
	if err := dec.Init(NewByteStreamInArray(out.GetData()), true); err != nil {
		t.Fatalf("dec.Init: %v", err)
	}
	icDec := NewIntegerDecompressor(dec, bits, contexts, bitsHigh, range_)
	icDec.InitDecompressor()
	for i, p := range pairs {
		got, err := icDec.Decompress(p.pred, uint32(i)%contexts)
		if err != nil {
			t.Fatalf("pair %d: Decompress: %v", i, err)
		}
		if got != p.real {
			t.Fatalf("pair %d: pred=%d real=%d: decompressed %d", i, p.pred, p.real, got)
		}
		if icDec.GetK() != encKs[i] {
			t.Fatalf("pair %d: GetK() mismatch: enc %d, dec %d", i, encKs[i], icDec.GetK())
		}
	}
}

// TestIntegerCompressorWriterBranches hits every writeCorrector branch:
// k==0 (delta 0/1), 0<k<=bitsHigh, bitsHigh<k<32 (two-step with raw low
// bits), and k==32 (nothing more written; decoder returns corrMin).
func TestIntegerCompressorWriterBranches(t *testing.T) {
	t.Run("bits32", func(t *testing.T) {
		pairs := []icPair{
			{0, 0},   // corr 0  → k=0, bit 0
			{0, 1},   // corr 1  → k=0, bit 1
			{5, 5},   // corr 0  → k=0
			{5, 4},   // corr -1 → k=1
			{0, 2},   // corr 2  → k=1 (c-1 == 1)
			{10, 8},  // corr -2 → k=2
			{0, 100}, // k=7 (<= bitsHigh)
			{0, 256}, // corr 256, c1=255 → k=8 (== bitsHigh)
			{0, 257}, // corr 257, c1=256 → k=9 (> bitsHigh: two-step)
			{0, -100},
			{0, 1 << 20},                           // k=20
			{0, -(1 << 20)},                        // negative large
			{0, (1 << 20) + 1},                     // k=21
			{0, 1<<30 + 12345},                     // k=30
			{-1000, 1<<30 + 12},                    // k=30-ish, negative pred
			{0, math.MaxInt32},                     // corr 2^31-1 → k=31
			{0, math.MinInt32},                     // corr -2^31 → k=32: nothing more written
			{1 << 30, -(1 << 30)},                  // corr wraps to -2^31 → k=32
			{math.MinInt32, math.MaxInt32},         // corr wraps to -1 → k=1
			{math.MaxInt32, math.MinInt32},         // corr wraps to 1 → k=0
			{math.MinInt32 + 5, math.MaxInt32 - 4}, // wraparound corr -10
			{42, 42},                               // trailing k=0 to check state after k=32 cases
		}
		icRoundTrip(t, 32, 2, 8, 0, pairs)
	})

	t.Run("bits16", func(t *testing.T) {
		// corrRange 65536, values must live in [0, 65536).
		pairs := []icPair{
			{0, 0},
			{0, 1},
			{100, 99},      // corr -1
			{100, 228},     // corr 128 → k=8
			{100, 356},     // corr 256 → k=8 boundary
			{100, 357},     // corr 257 → k=9 two-step
			{0, 65535},     // corr folds to -1
			{65535, 0},     // corr folds to +1
			{0, 32768},     // corr ±32768 boundary (corrMin)
			{32768, 0},     // corr -32768 → folds
			{12345, 54321}, // large positive corr folds negative
			{54321, 12345}, // large negative corr folds positive
			{40000, 40000},
		}
		icRoundTrip(t, 16, 4, 8, 0, pairs)
	})

	t.Run("bits8", func(t *testing.T) {
		// corrRange 256, values must live in [0, 256).
		pairs := []icPair{
			{0, 0},
			{0, 1},
			{128, 127},
			{0, 255}, // folds to -1
			{255, 0}, // folds to +1
			{0, 128}, // corrMin boundary
			{200, 60},
			{60, 200},
		}
		icRoundTrip(t, 8, 1, 8, 0, pairs)
	})

	t.Run("explicitRange", func(t *testing.T) {
		// Explicit non-power-of-two range (like the POINT10 z decompressor
		// uses in places). Values must live in [0, range).
		const rng = 1000000
		pairs := []icPair{
			{0, 0},
			{0, 1},
			{500000, 499999},
			{0, 999999}, // folds
			{999999, 0}, // folds
			{123456, 654321},
			{654321, 123456},
		}
		icRoundTrip(t, 0, 2, 8, rng, pairs)
	})
}

// TestIntegerCompressorWriterRandom round-trips long random sequences for
// several bit widths so the entropy models cross many update cycles.
func TestIntegerCompressorWriterRandom(t *testing.T) {
	cases := []struct {
		name     string
		bits     uint32
		contexts uint32
		range_   uint32
		n        int
		seed     int64
	}{
		{"bits8", 8, 2, 0, 20000, 7},
		{"bits16", 16, 4, 0, 20000, 8},
		{"bits32", 32, 2, 0, 20000, 9},
		{"range1000000", 0, 3, 1000000, 10000, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(tc.seed))
			var domain int64
			if tc.range_ != 0 {
				domain = int64(tc.range_)
			} else if tc.bits < 32 {
				domain = int64(1) << tc.bits
			} else {
				domain = 1 << 32
			}
			pairs := make([]icPair, tc.n)
			last := int32(0)
			for i := range pairs {
				var real int32
				if tc.bits == 32 && tc.range_ == 0 {
					real = int32(rng.Uint32())
				} else {
					real = int32(rng.Int63n(domain))
				}
				// Mix small deltas (predictable) with random jumps so all
				// k values occur with realistic frequencies.
				if rng.Intn(3) != 0 {
					delta := int32(rng.Intn(65)) - 32
					real = last + delta
					if tc.bits != 32 || tc.range_ != 0 {
						if real < 0 {
							real += int32(domain)
						} else if int64(real) >= domain {
							real -= int32(domain)
						}
					}
				}
				pairs[i] = icPair{pred: last, real: real}
				last = real
			}
			icRoundTrip(t, tc.bits, tc.contexts, 8, tc.range_, pairs)
		})
	}
}
