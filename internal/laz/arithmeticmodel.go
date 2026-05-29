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
// arithmeticmodel.go — ArithmeticModel (N-ary symbols) and
// ArithmeticBitModel (single bits) ported from src/arithmeticmodel.hpp/cpp.
// These implement Amir Said's FastAC adaptive probability models.
package laz

// ---------------------------------------------------------------------------
// Constants (from arithmeticmodel.hpp)
// ---------------------------------------------------------------------------

const (
	AC__MinLength   uint32 = 0x01000000 // threshold for renormalization
	AC__MaxLength   uint32 = 0xFFFFFFFF // maximum AC interval length
	BM__LengthShift uint32 = 13         // length bits discarded before mult.
	BM__MaxCount    uint32 = 1 << BM__LengthShift
	DM__LengthShift uint32 = 15 // length bits discarded before mult.
	DM__MaxCount    uint32 = 1 << DM__LengthShift
)

// ---------------------------------------------------------------------------
// ArithmeticBitModel — single-bit adaptive probability model
// ---------------------------------------------------------------------------

// ArithmeticBitModel is a single-bit adaptive probability model used by the
// arithmetic decoder. It tracks the probability of bit_0 and periodically
// updates based on observed frequencies.
type ArithmeticBitModel struct {
	Bit0Prob        uint32 // scaled probability of bit 0 (C++: bit_0_prob)
	Bit0Count       uint32 // count of 0 bits seen (C++: bit_0_count)
	BitCount        uint32 // total count of bits seen (C++: bit_count)
	UpdateCycle     uint32 // how often to update (C++: update_cycle)
	BitsUntilUpdate uint32 // countdown to next update (C++: bits_until_update)
}

// NewArithmeticBitModel creates and initializes a new bit model.
func NewArithmeticBitModel() *ArithmeticBitModel {
	m := &ArithmeticBitModel{}
	m.Init()
	return m
}

// Init initializes the bit model to equiprobable (p0 = 0.5).
func (m *ArithmeticBitModel) Init() {
	m.Bit0Count = 1
	m.BitCount = 2
	m.Bit0Prob = 1 << (BM__LengthShift - 1) // 1 << (13-1) = 4096 = 0x1000
	m.UpdateCycle = 4
	m.BitsUntilUpdate = 4
}

// Update updates the probability model based on observed counts.
// This is called when BitsUntilUpdate reaches 0.
func (m *ArithmeticBitModel) Update() {
	// Halve counts when a threshold is reached.
	m.BitCount += m.UpdateCycle
	if m.BitCount > BM__MaxCount {
		m.BitCount = (m.BitCount + 1) >> 1
		m.Bit0Count = (m.Bit0Count + 1) >> 1
		if m.Bit0Count == m.BitCount {
			m.BitCount++
		}
	}

	// Compute scaled bit 0 probability.
	scale := uint32(0x80000000) / m.BitCount
	m.Bit0Prob = (m.Bit0Count * scale) >> (31 - BM__LengthShift)

	// Set frequency of model updates.
	m.UpdateCycle = min((5*m.UpdateCycle)>>2, 64)
	m.BitsUntilUpdate = m.UpdateCycle
}

// ---------------------------------------------------------------------------
// ArithmeticModel — N-ary symbol adaptive probability model
// ---------------------------------------------------------------------------

// ArithmeticModel is an N-ary symbol adaptive probability model used by the
// arithmetic decoder. It maintains a cumulative distribution and optionally
// a decoder table for fast look-up-based decoding.
type ArithmeticModel struct {
	Distribution       []uint32 // cumulative distribution (C++: distribution)
	SymbolCount        []uint32 // per-symbol counts (C++: symbol_count)
	DecoderTable       []uint32 // decoder look-up table (C++: decoder_table), nil if not used
	TotalCount         uint32   // sum of all symbol counts (C++: total_count)
	UpdateCycle        uint32   // how often to update (C++: update_cycle)
	SymbolsUntilUpdate uint32   // countdown to next update (C++: symbols_until_update)
	Symbols            uint32   // number of symbols in the alphabet
	LastSymbol         uint32   // last symbol index (= symbols - 1)
	TableSize          uint32   // size of the decoder table
	TableShift         uint32   // shift amount for table indexing
}

// NewArithmeticModel creates a new symbol model for the given alphabet size.
// Symbols must be >= 2 and <= 2048.
func NewArithmeticModel(symbols uint32) *ArithmeticModel {
	return &ArithmeticModel{
		Symbols: symbols,
	}
}

// Init initializes the symbol model. If table is non-nil, those counts are used;
// otherwise all counts start at 1.
func (m *ArithmeticModel) Init(table []uint32) {
	if m.Distribution == nil {
		symbols := m.Symbols
		if symbols < 2 || symbols > (1<<11) {
			panic("invalid number of symbols")
		}
		m.LastSymbol = symbols - 1

		// For decompression with more than 16 symbols, build a decoder table.
		if symbols > 16 {
			tableBits := uint32(3)
			for symbols > (1 << (tableBits + 2)) {
				tableBits++
			}
			m.TableSize = 1 << tableBits
			m.TableShift = DM__LengthShift - tableBits
			// Allocation: 2*symbols + tableSize + 2
			m.Distribution = make([]uint32, 2*symbols+m.TableSize+2)
			m.DecoderTable = m.Distribution[2*symbols:]
			m.SymbolCount = m.Distribution[symbols : 2*symbols]
		} else {
			// Small alphabet: no table needed.
			m.DecoderTable = nil
			m.TableSize = 0
			m.TableShift = 0
			m.Distribution = make([]uint32, 2*symbols)
			m.SymbolCount = m.Distribution[symbols : 2*symbols]
		}
	}

	m.TotalCount = 0
	m.UpdateCycle = m.Symbols

	if table != nil {
		copy(m.SymbolCount, table)
	} else {
		for k := uint32(0); k < m.Symbols; k++ {
			m.SymbolCount[k] = 1
		}
	}

	m.update()
	m.UpdateCycle = (m.Symbols + 6) >> 1
	m.SymbolsUntilUpdate = m.UpdateCycle
}

// update recomputes the cumulative distribution and decoder table.
// This is ported exactly from ArithmeticModel::update().
func (m *ArithmeticModel) update() {
	// Halve counts when a threshold is reached.
	m.TotalCount += m.UpdateCycle
	if m.TotalCount > DM__MaxCount {
		m.TotalCount = 0
		for n := uint32(0); n < m.Symbols; n++ {
			m.SymbolCount[n] = (m.SymbolCount[n] + 1) >> 1
			m.TotalCount += m.SymbolCount[n]
		}
	}

	// Compute cumulative distribution and decoder table.
	var k, sum, s uint32
	scale := uint32(0x80000000) / m.TotalCount

	if m.DecoderTable == nil {
		for k = 0; k < m.Symbols; k++ {
			m.Distribution[k] = (scale * sum) >> (31 - DM__LengthShift)
			sum += m.SymbolCount[k]
		}
	} else {
		for k = 0; k < m.Symbols; k++ {
			m.Distribution[k] = (scale * sum) >> (31 - DM__LengthShift)
			sum += m.SymbolCount[k]
			w := m.Distribution[k] >> m.TableShift
			for s < w {
				s++
				m.DecoderTable[s] = k - 1
			}
		}
		m.DecoderTable[0] = 0
		for s <= m.TableSize {
			s++
			m.DecoderTable[s] = m.Symbols - 1
		}
	}

	// Set frequency of model updates.
	m.UpdateCycle = (5 * m.UpdateCycle) >> 2
	maxCycle := (m.Symbols + 6) << 3
	if m.UpdateCycle > maxCycle {
		m.UpdateCycle = maxCycle
	}
	m.SymbolsUntilUpdate = m.UpdateCycle
}
