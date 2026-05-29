// Copyright (c) 2007-2022 rapidlasso GmbH - fast tools to catch reality (Original C++ implementation)
// Copyright (c) 2026 Massimo Federico Bonfigli (Go port and modifications)
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
//
// This file is a Go port of LASzip (https://github.com/LASzip/LASzip).
// Changes: translated from C++ to Go.

// Package laz provides LAZ (LASzip) decompression for LAS point cloud data.
//
// integercompressor.go — IntegerCompressor ported from
// src/integercompressor.hpp/cpp. Decompression path only.
// Implements predictive difference coding for integer values, where the
// prediction residual (corrector) is entropy-coded using k-bit intervals.
package laz

import "fmt"

// IntegerCompressor provides decompression of integer numbers using
// predictive difference coding with multiple contexts. The compressor encodes
// two things: (1) the number k of miss-predicted low-order bits, and (2)
// the k-bit number that corrects the missprediction.
type IntegerCompressor struct {
	dec *ArithmeticDecoder

	k uint32 // number of correction bits from last decode

	contexts  uint32
	bitsHigh  uint32
	bits      uint32
	range_    uint32
	corrBits  uint32
	corrRange uint32
	corrMin   int32
	corrMax   int32

	// mBits[context] — symbol model for decoding k (0..corrBits)
	mBits []*ArithmeticModel

	// mCorrectorBit — bit model for k==0 case (decodes 0 or 1)
	mCorrectorBit *ArithmeticBitModel
	// mCorrector[k] — symbol model for decoding the corrector when 1 <= k <= corrBits
	mCorrector []*ArithmeticModel
}

// NewIntegerDecompressor creates a new IntegerCompressor for decompression.
//
// Parameters (matching C++ constructor):
//
//	dec:       the ArithmeticDecoder to read from
//	bits:      number of bits for the integer (default 16)
//	contexts:   number of contexts (default 1)
//	bitsHigh:  how many of the higher bits are compressed with a symbol model (default 8)
//	range_:    explicit range for the corrector (0 = derive from bits)
func NewIntegerDecompressor(dec *ArithmeticDecoder, bits, contexts, bitsHigh, range_ uint32) *IntegerCompressor {
	ic := &IntegerCompressor{
		dec:      dec,
		bits:     bits,
		contexts: contexts,
		bitsHigh: bitsHigh,
		range_:   range_,
	}

	// Compute corrector range and bounds (identical to C++ constructor).
	if range_ != 0 {
		ic.corrBits = 0
		ic.corrRange = range_
		r := range_
		for r != 0 {
			r >>= 1
			ic.corrBits++
		}
		if ic.corrRange == (1 << (ic.corrBits - 1)) {
			ic.corrBits--
		}
		ic.corrMin = -int32(ic.corrRange / 2)
		ic.corrMax = ic.corrMin + int32(ic.corrRange) - 1
	} else if bits != 0 && bits < 32 {
		ic.corrBits = bits
		ic.corrRange = 1 << bits
		ic.corrMin = -int32(ic.corrRange / 2)
		ic.corrMax = ic.corrMin + int32(ic.corrRange) - 1
	} else {
		ic.corrBits = 32
		ic.corrRange = 0
		ic.corrMin = -2147483648 // int32 min
		ic.corrMax = 2147483647  // int32 max
	}

	return ic
}

// InitDecompressor creates and initializes all entropy models.
// Must be called before Decompress.
func (ic *IntegerCompressor) InitDecompressor() {
	if ic.dec == nil {
		panic("IntegerCompressor: InitDecompressor called but dec is nil")
	}

	// Create mBits models (one per context, for decoding k).
	if ic.mBits == nil {
		ic.mBits = make([]*ArithmeticModel, ic.contexts)
		for i := uint32(0); i < ic.contexts; i++ {
			ic.mBits[i] = ic.dec.CreateSymbolModel(ic.corrBits + 1)
		}
		// Create mCorrector models (for decoding the corrector value).
		ic.mCorrectorBit = ic.dec.CreateBitModel()
		ic.mCorrector = make([]*ArithmeticModel, ic.corrBits+1)
		// mCorrector[0] is unused; we use mCorrectorBit instead.
		for i := uint32(1); i <= ic.corrBits; i++ {
			if i <= ic.bitsHigh {
				ic.mCorrector[i] = ic.dec.CreateSymbolModel(1 << i)
			} else {
				ic.mCorrector[i] = ic.dec.CreateSymbolModel(1 << ic.bitsHigh)
			}
		}
	}

	// Init mBits models.
	for i := uint32(0); i < ic.contexts; i++ {
		ic.dec.InitSymbolModel(ic.mBits[i], nil)
	}
	// Init mCorrector models.
	ic.dec.InitBitModel(ic.mCorrectorBit)
	for i := uint32(1); i <= ic.corrBits; i++ {
		ic.dec.InitSymbolModel(ic.mCorrector[i], nil)
	}
}

// Decompress decodes the integer value given a predicted value and context.
// Returns real = iPred + corrector, adjusted to stay within the corrector range.
func (ic *IntegerCompressor) Decompress(iPred int32, context uint32) (int32, error) {
	if ic.dec == nil {
		return 0, fmt.Errorf("IntegerCompressor: dec is nil")
	}
	corr, err := ic.readCorrector(ic.mBits[context])
	if err != nil {
		return 0, err
	}
	real := iPred + corr
	if real < 0 {
		real += int32(ic.corrRange)
	} else if uint32(real) >= ic.corrRange {
		real -= int32(ic.corrRange)
	}
	return real, nil
}

// GetK returns the number of correction bits from the last Decompress call.
func (ic *IntegerCompressor) GetK() uint32 { return ic.k }

// readCorrector decodes the corrector value from the bit stream.
// This is the port of IntegerCompressor::readCorrector() — the !COMPRESS_ONLY_K path.
func (ic *IntegerCompressor) readCorrector(mBits *ArithmeticModel) (int32, error) {
	var c int32

	// Decode within which interval the corrector is falling.
	k, err := ic.dec.DecodeSymbol(mBits)
	if err != nil {
		return 0, fmt.Errorf("readCorrector: decode k: %w", err)
	}
	ic.k = k

	// Decode the exact location of the corrector within the interval.
	if k != 0 {
		// c is either smaller than 0 or bigger than 1.
		if k < 32 {
			if k <= ic.bitsHigh {
				// For small k we can do this in one step.
				sym, err := ic.dec.DecodeSymbol(ic.mCorrector[k])
				if err != nil {
					return 0, fmt.Errorf("readCorrector: decodeSymbol k=%d: %w", k, err)
				}
				c = int32(sym)
			} else {
				// For larger k we need two steps.
				k1 := k - ic.bitsHigh
				// Decompress higher bits with table.
				sym, err := ic.dec.DecodeSymbol(ic.mCorrector[k])
				if err != nil {
					return 0, fmt.Errorf("readCorrector: decodeSymbol k=%d: %w", k, err)
				}
				// Read lower bits raw.
				c1, err := ic.dec.ReadBits(k1)
				if err != nil {
					return 0, fmt.Errorf("readCorrector: readBits k1=%d: %w", k1, err)
				}
				// Put the corrector back together.
				c = (int32(sym) << k1) | int32(c1)
			}
			// Translate c back into its correct interval.
			if uint32(c) >= (1 << (k - 1)) {
				// c is in [2^(k-1), 2^k-1] → translate to [2^(k-1)+1, 2^k]
				c += 1
			} else {
				// c is in [0, 2^(k-1)-1] → translate to [-(2^k-1), -2^(k-1)]
				c -= int32((1 << k) - 1)
			}
		} else {
			c = ic.corrMin
		}
	} else {
		// k == 0: c is either 0 or 1.
		bit, err := ic.dec.DecodeBit(ic.mCorrectorBit)
		if err != nil {
			return 0, fmt.Errorf("readCorrector: decodeBit: %w", err)
		}
		c = int32(bit)
	}

	return c, nil
}
