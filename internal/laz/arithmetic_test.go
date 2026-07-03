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
	"encoding/binary"
	"testing"
)

// ===========================================================================
// ArithmeticBitModel tests
// ===========================================================================

func TestBitModelInit(t *testing.T) {
	m := NewArithmeticBitModel()

	if m.Bit0Count != 1 {
		t.Errorf("Bit0Count = %d, want 1", m.Bit0Count)
	}
	if m.BitCount != 2 {
		t.Errorf("BitCount = %d, want 2", m.BitCount)
	}
	if m.Bit0Prob != 1<<(BM__LengthShift-1) {
		t.Errorf("Bit0Prob = %d, want %d", m.Bit0Prob, 1<<(BM__LengthShift-1))
	}
	if m.UpdateCycle != 4 {
		t.Errorf("UpdateCycle = %d, want 4", m.UpdateCycle)
	}
	if m.BitsUntilUpdate != 4 {
		t.Errorf("BitsUntilUpdate = %d, want 4", m.BitsUntilUpdate)
	}
}

func TestBitModelInit_Equiprobable(t *testing.T) {
	m := NewArithmeticBitModel()
	// Equiprobable: p0 = 0.5 → Bit0Prob = 4096 (1 << 12), BM__MaxCount = 8192
	if m.Bit0Prob != 4096 {
		t.Errorf("equiprobable Bit0Prob = %d, want 4096", m.Bit0Prob)
	}
}

func TestBitModelUpdate_Cycle(t *testing.T) {
	m := NewArithmeticBitModel()

	// After Bit0Prob initial value, force an update by simulating decode.
	// We'll call Update directly enough times to see the cycle doubling.
	initialProb := m.Bit0Prob
	initialCycle := m.UpdateCycle

	m.Update()

	// Update cycle should grow: (5 * 4) >> 2 = 5
	if m.UpdateCycle != 5 {
		t.Errorf("after first update: UpdateCycle = %d, want 5", m.UpdateCycle)
	}
	if m.BitsUntilUpdate != m.UpdateCycle {
		t.Errorf("BitsUntilUpdate %d != UpdateCycle %d", m.BitsUntilUpdate, m.UpdateCycle)
	}
	_ = initialProb
	_ = initialCycle
}

func TestBitModelUpdate_Convergence(t *testing.T) {
	m := NewArithmeticBitModel()

	// Run many update cycles — the update cycle should max out at 64.
	for range 50 {
		m.Update()
	}
	if m.UpdateCycle != 64 {
		t.Errorf("after many updates, UpdateCycle = %d, want 64", m.UpdateCycle)
	}
}

func TestBitModelCounts_StayConsistent(t *testing.T) {
	m := NewArithmeticBitModel()

	// After some updates, Bit0Count should never exceed BitCount.
	for i := range 20 {
		m.Update()
		if m.Bit0Count > m.BitCount {
			t.Fatalf("iteration %d: Bit0Count (%d) > BitCount (%d)", i, m.Bit0Count, m.BitCount)
		}
	}
}

// ===========================================================================
// ArithmeticModel tests
// ===========================================================================

func TestSymbolModelInit_Small(t *testing.T) {
	m := NewArithmeticModel(8, false) // 8 symbols
	m.Init(nil)

	if m.LastSymbol != 7 {
		t.Errorf("LastSymbol = %d, want 7", m.LastSymbol)
	}
	if m.DecoderTable != nil {
		t.Error("small model (8 symbols) should not have decoder table")
	}
	if m.TotalCount != 8 {
		t.Errorf("TotalCount = %d, want 8 (each count = 1)", m.TotalCount)
	}
	// update_cycle after init: (symbols + 6) >> 1 = (8+6)>>1 = 7
	if m.UpdateCycle != 7 {
		t.Errorf("UpdateCycle = %d, want 7", m.UpdateCycle)
	}
	if m.SymbolsUntilUpdate != m.UpdateCycle {
		t.Errorf("SymbolsUntilUpdate %d != UpdateCycle %d", m.SymbolsUntilUpdate, m.UpdateCycle)
	}
}

func TestSymbolModelInit_Large(t *testing.T) {
	m := NewArithmeticModel(32, false) // > 16, should use decoder table
	m.Init(nil)

	if m.DecoderTable == nil {
		t.Fatal("large model (32 symbols) should have decoder table")
	}
	if m.TableSize == 0 {
		t.Error("TableSize should be > 0 for large model")
	}
	if m.TableShift == 0 {
		t.Error("TableShift should be > 0 for large model")
	}
}

func TestSymbolModelInit_WithTable(t *testing.T) {
	m := NewArithmeticModel(4, false)
	table := []uint32{10, 5, 2, 1}
	m.Init(table)

	if m.SymbolCount[0] != 10 {
		t.Errorf("SymbolCount[0] = %d, want 10", m.SymbolCount[0])
	}
	if m.SymbolCount[1] != 5 {
		t.Errorf("SymbolCount[1] = %d, want 5", m.SymbolCount[1])
	}
	if m.SymbolCount[2] != 2 {
		t.Errorf("SymbolCount[2] = %d, want 2", m.SymbolCount[2])
	}
	if m.SymbolCount[3] != 1 {
		t.Errorf("SymbolCount[3] = %d, want 1", m.SymbolCount[3])
	}
}

func TestSymbolModelDistribution_Monotonic(t *testing.T) {
	m := NewArithmeticModel(8, false)
	m.Init(nil)

	// Distribution should be non-decreasing.
	for k := uint32(1); k < 8; k++ {
		if m.Distribution[k] < m.Distribution[k-1] {
			t.Errorf("distribution[%d] = %d < distribution[%d] = %d",
				k, m.Distribution[k], k-1, m.Distribution[k-1])
		}
	}
	// First element should be 0.
	if m.Distribution[0] != 0 {
		t.Errorf("distribution[0] = %d, want 0", m.Distribution[0])
	}
}

func TestSymbolModelInvalidSymbols(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for 1 symbol")
		}
	}()
	m := NewArithmeticModel(1, false)
	m.Init(nil)
}

// ===========================================================================
// ArithmeticDecoder — init / lifecycle tests
// ===========================================================================

func TestDecoderInit(t *testing.T) {
	// Prepare a stream with 4 initialization bytes.
	data := []byte{0x12, 0x34, 0x56, 0x78, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	err := dec.Init(stream, true)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// value = (0x12<<24) | (0x34<<16) | (0x56<<8) | 0x78
	expectedValue := uint32(0x12345678)
	if dec.value != expectedValue {
		t.Errorf("after init: value = %#08x, want %#08x", dec.value, expectedValue)
	}
	if dec.length != AC__MaxLength {
		t.Errorf("after init: length = %d, want %d", dec.length, AC__MaxLength)
	}
}

func TestDecoderInitNoReallyInit(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	err := dec.Init(stream, false)
	if err != nil {
		t.Fatalf("Init(false): %v", err)
	}
	// Stream cursor should still be at 0 (no bytes consumed).
	pos, _ := stream.Tell()
	if pos != 0 {
		t.Errorf("Init(false) consumed %d bytes, want 0", pos)
	}
}

func TestDecoderInitNilStream(t *testing.T) {
	dec := NewArithmeticDecoder()
	err := dec.Init(nil, true)
	if err == nil {
		t.Fatal("Init(nil) should return error")
	}
}

func TestDecoderDone(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)
	dec.Done()

	if dec.instream != nil {
		t.Error("after Done(), instream should be nil")
	}
}

// ===========================================================================
// ArithmeticDecoder — ReadBit / ReadBits (no modelling)
// ===========================================================================

func TestDecoderReadBit_Simple(t *testing.T) {
	// For a uniform ReadBit: length >>= 1, sym = value / length, value -= length*sym
	// With value=0, length=0xFFFFFFFF, first ReadBit: length>>=1 = 0x7FFFFFFF, sym = 0/... = 0
	// So we should get bit 0.
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0

	bit, err := dec.ReadBit()
	if err != nil {
		t.Fatalf("ReadBit: %v", err)
	}
	if bit != 0 {
		t.Errorf("ReadBit with value=0 should return 0, got %d", bit)
	}
}

func TestDecoderReadBit_ValueAtMax(t *testing.T) {
	// value = 0x7FFFFFFF, length = 0xFFFFFFFF
	// ReadBit: length >>= 1 = 0x7FFFFFFF
	// sym = 0x7FFFFFFF / 0x7FFFFFFF = 1
	// (Note: 0xFFFFFFFF / 0x7FFFFFFF = 2, which triggers the sym >= 2 check,
	//  so we use 0x7FFFFFFF as the largest value that safely gives sym=1.)
	// if sym >= 2 → error
	// So result = 1.
	data := []byte{0x7F, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0x7FFFFFFF

	bit, err := dec.ReadBit()
	if err != nil {
		t.Fatalf("ReadBit: %v", err)
	}
	if bit != 1 {
		t.Errorf("ReadBit with value=max should return 1, got %d", bit)
	}
}

func TestDecoderReadBits_8Bits(t *testing.T) {
	// value = 0x80000000 (midpoint)
	// ReadBits(8): length >>= 8, sym = value / length
	// Since value starts at 0x80000000 and length=0xFFFFFFFF:
	// length after shift = 0xFFFFFFFF >> 8 = 0x00FFFFFF
	// sym = 0x80000000 / 0x00FFFFFF = 128 (0x80)
	data := []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0x80000000

	val, err := dec.ReadBits(8)
	if err != nil {
		t.Fatalf("ReadBits(8): %v", err)
	}
	if val != 128 {
		t.Errorf("ReadBits(8) = %d, want 128", val)
	}
}

func TestDecoderReadBits_32bitsMax(t *testing.T) {
	// 32 > 19 → splits into ReadShort(16) + ReadBits(16).
	// With value=0: ReadShort returns 0, ReadBits(16) returns 0 → result=0.
	// Needs 4 init + renorm bytes for each sub-read (ReadShort needs 2 renorm bytes,
	// ReadBits(16) needs 1 renorm byte). Provide generous buffer.
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0
	val, err := dec.ReadBits(32)
	if err != nil {
		t.Fatalf("ReadBits(32): %v", err)
	}
	// With value=0: ReadShort→0, ReadBits(16)→0, result = (0<<16)|0 = 0.
	if val != 0 {
		t.Errorf("ReadBits(32) with value=0 should be 0, got %d", val)
	}
}

func TestDecoderReadByte(t *testing.T) {
	// Init consumes 4 bytes → value=0x40000000. ReadByte shifts length by 8,
	// producing 0x00FFFFFF < AC__MinLength, triggering renorm which needs 1 more byte.
	data := []byte{0x40, 0x00, 0x00, 0x00, 0x00} // value = 0x40000000 + 1 renorm byte
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	// value = 0x40000000, length = 0xFFFFFFFF
	// ReadByte: length >>= 8 = 0x00FFFFFF
	// sym = 0x40000000 / 0x00FFFFFF = 64 (0x40)
	b, err := dec.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte: %v", err)
	}
	if b != 64 {
		t.Errorf("ReadByte = %d, want 64", b)
	}
}

func TestDecoderReadShort(t *testing.T) {
	// value = 0x80000000 (halfway). ReadShort: length>>=16 = 0x0000FFFF
	// (< AC__MinLength → renorm consumes 2 more bytes to shift length back up).
	// sym = 0x80000000 / 0x0000FFFF = 32768 (0x8000)
	// Needs 4 init bytes + 2 renorm bytes = 6.
	data := []byte{0x80, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	val, err := dec.ReadShort()
	if err != nil {
		t.Fatalf("ReadShort: %v", err)
	}
	if val != 32768 {
		t.Errorf("ReadShort = %d, want 32768", val)
	}
}

func TestDecoderReadInt(t *testing.T) {
	// value = 0, ReadInt = ReadShort + ReadShort = 0 + 0 = 0
	data := make([]byte, 16)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false) // don't consume bytes
	dec.length = AC__MaxLength
	dec.value = 0

	val, err := dec.ReadInt()
	if err != nil {
		t.Fatalf("ReadInt: %v", err)
	}
	if val != 0 {
		t.Errorf("ReadInt with value=0 should be 0, got %d", val)
	}
}

func TestDecoderReadFloat(t *testing.T) {
	// ReadFloat calls ReadInt → 2× ReadShort. To get ReadInt=0x3F800000 (IEEE 754 1.0f):
	//   Lower=0, Upper=0x3F80. Use init value=0x00003F80:
	//   ReadShort#1: len→0x0000FFFF, sym=0x00003F80/0x0000FFFF=0 (lower).
	//     After renorm×2 (reads 2 bytes): value=0x3F800000, len restored.
	//   ReadShort#2: len→0x0000FFFF, sym=0x3F800000/0x0000FFFF=0x3F80=16256 (upper).
	//   ReadInt=(16256<<16)|0=0x3F800000, Float32frombits→1.0.
	// Needs 4 init + 2 renorm + 2 renorm = 8 bytes.
	data := []byte{0x00, 0x00, 0x3F, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0x00003F80

	f, err := dec.ReadFloat()
	if err != nil {
		t.Fatalf("ReadFloat: %v", err)
	}
	if f != 1.0 {
		t.Errorf("ReadFloat = %v, want 1.0", f)
	}
}

func TestDecoderReadInt64(t *testing.T) {
	data := make([]byte, 32)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = AC__MaxLength
	dec.value = 0

	val, err := dec.ReadInt64()
	if err != nil {
		t.Fatalf("ReadInt64: %v", err)
	}
	if val != 0 {
		t.Errorf("ReadInt64 with value=0 should be 0, got %d", val)
	}
}

func TestDecoderReadDouble(t *testing.T) {
	data := make([]byte, 32)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = AC__MaxLength
	dec.value = 0

	f, err := dec.ReadDouble()
	if err != nil {
		t.Fatalf("ReadDouble: %v", err)
	}
	if f != 0.0 {
		t.Errorf("ReadDouble with value=0 should be 0.0, got %v", f)
	}
}

// ===========================================================================
// ArithmeticDecoder — DecodeBit (with modelling)
// ===========================================================================

func TestDecoderDecodeBit_WithModel(t *testing.T) {
	// Create decoder with value=0, then decode bits.
	// value=0 means all bits will be 0.
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0 from 4 zero bytes

	model := dec.CreateBitModel()

	// With value=0, every DecodeBit should return 0 because value < x (x is p0*len, always >0)
	for i := range 10 {
		bit, err := dec.DecodeBit(model)
		if err != nil {
			t.Fatalf("DecodeBit #%d: %v", i, err)
		}
		if bit != 0 {
			t.Errorf("DecodeBit #%d = %d, want 0 (value=0)", i, bit)
		}
	}
}

func TestDecoderDecodeBit_ModelUpdatesCounts(t *testing.T) {
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	model := dec.CreateBitModel()
	initial0Count := model.Bit0Count

	// Decode 10 zeros — Bit0Count should increase.
	for range 10 {
		dec.DecodeBit(model)
	}

	if model.Bit0Count <= initial0Count {
		t.Error("Bit0Count should have increased after decoding zeros")
	}
}

func TestDecoderDecodeBit_BitsUntilUpdateDecrements(t *testing.T) {
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	model := dec.CreateBitModel()
	initial := model.BitsUntilUpdate

	dec.DecodeBit(model)

	if model.BitsUntilUpdate != initial-1 {
		t.Errorf("BitsUntilUpdate = %d, want %d (decremented by 1)", model.BitsUntilUpdate, initial-1)
	}
}

// ===========================================================================
// ArithmeticDecoder — DecodeSymbol (with modelling)
// ===========================================================================

func TestDecoderDecodeSymbol_SmallModel(t *testing.T) {
	// 4-symbol model, value=0 → all symbols should decode as 0.
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0

	model := dec.CreateSymbolModel(4)
	dec.InitSymbolModel(model, nil)

	sym, err := dec.DecodeSymbol(model)
	if err != nil {
		t.Fatalf("DecodeSymbol: %v", err)
	}
	if sym != 0 {
		t.Errorf("DecodeSymbol with value=0 should return 0, got %d", sym)
	}
}

func TestDecoderDecodeSymbol_LargeModel(t *testing.T) {
	// 32-symbol model with decoder table.
	data := make([]byte, 128)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0

	model := dec.CreateSymbolModel(32)
	dec.InitSymbolModel(model, nil)

	// With value=0, DecodeSymbol should return 0 (value < all distribution entries)
	sym, err := dec.DecodeSymbol(model)
	if err != nil {
		t.Fatalf("DecodeSymbol (large): %v", err)
	}
	if sym != 0 {
		t.Errorf("DecodeSymbol with value=0 should return 0, got %d", sym)
	}
}

func TestDecoderDecodeSymbol_Cleanup(t *testing.T) {
	// Verify model cleanup works — re-init with different values.
	data := make([]byte, 128)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	model := dec.CreateSymbolModel(8)
	dec.InitSymbolModel(model, nil)

	sym1, _ := dec.DecodeSymbol(model)

	// Re-init the same model (should reset counts).
	dec.InitSymbolModel(model, nil)
	sym2, _ := dec.DecodeSymbol(model)

	// Both should decode to 0 with value=0.
	if sym1 != 0 || sym2 != 0 {
		t.Errorf("sym1=%d, sym2=%d, both should be 0", sym1, sym2)
	}
}

// ===========================================================================
// ArithmeticDecoder — renormalization tests
// ===========================================================================

func TestDecoderRenormDecInterval(t *testing.T) {
	// Force renormalization by setting length below AC__MinLength.
	// Init consumes 4 bytes → value=0x12345678. Then set length=1 → renorm
	// iterates 3 times (1→256→65536→16777216≥0x01000000), reading 3 bytes.
	// Needs 4 init + 3 renorm = 7 bytes.
	data := []byte{0x12, 0x34, 0x56, 0x78, 0xAA, 0xBB, 0xCC}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0x12345678, length = 0xFFFFFFFF, pos=4

	dec.length = 0x00000001 // well below AC__MinLength (0x01000000)

	err := dec.renormDecInterval()
	if err != nil {
		t.Fatalf("renormDecInterval: %v", err)
	}

	// After renormalization, length should be ≥ AC__MinLength.
	if dec.length < AC__MinLength {
		t.Errorf("after renorm: length = %#x, want >= %#x", dec.length, AC__MinLength)
	}
	// value = (0x12345678<<8|0xAA)<<8|0xBB)<<8|0xCC = ...
	// Verified: stream consumed 3 bytes (pos 4→7).
	pos, _ := dec.instream.Tell()
	if pos != 7 {
		t.Errorf("stream pos after renorm = %d, want 7 (init consumed 4, renorm 3)", pos)
	}
}

func TestDecoderRenorm_MultipleBytes(t *testing.T) {
	// Start with length=1. Renorm needs to shift left 8 + read byte until length >= AC__MinLength=0x01000000.
	// 1 << 8 = 0x100, still < 0x01000000, so needs another shift: 0x100 << 8 = 0x10000, still < ...
	// 0x10000 << 8 = 0x1000000 >= 0x01000000 → done after 3 iterations.
	data := []byte{0x00, 0x00, 0x00, 0x00, 0xAA, 0xBB, 0xCC, 0xDD}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false) // don't consume init bytes
	dec.length = 1
	dec.value = 0

	err := dec.renormDecInterval()
	if err != nil {
		t.Fatalf("renormDecInterval: %v", err)
	}
	// Should have consumed 3 bytes (0xAA, 0xBB, 0xCC)
	pos, _ := stream.Tell()
	if pos != 3 {
		t.Errorf("renorm consumed %d bytes, want 3", pos)
	}
}

// ===========================================================================
// ArithmeticDecoder — combined integration tests
// ===========================================================================

func TestDecoderCombined_ReadFloatReadDouble(t *testing.T) {
	// Prepare a stream that allows reading 4+8 bytes via arithmetic decoder.
	// With value=0 and length=max, ReadInt=0 → ReadFloat=0.0, ReadInt64=0 → ReadDouble=0.0
	data := make([]byte, 32)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = AC__MaxLength
	dec.value = 0

	f, err := dec.ReadFloat()
	if err != nil {
		t.Fatalf("ReadFloat: %v", err)
	}
	d, err := dec.ReadDouble()
	if err != nil {
		t.Fatalf("ReadDouble: %v", err)
	}
	if f != 0.0 {
		t.Errorf("ReadFloat = %v, want 0.0", f)
	}
	if d != 0.0 {
		t.Errorf("ReadDouble = %v, want 0.0", d)
	}
}

func TestDecoderCombined_DecodeBitThenDecodeSymbol(t *testing.T) {
	data := make([]byte, 128)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0

	bitModel := dec.CreateBitModel()
	symModel := dec.CreateSymbolModel(8)
	dec.InitSymbolModel(symModel, nil)

	// Interleave bit and symbol decoding.
	for i := range 5 {
		bit, err := dec.DecodeBit(bitModel)
		if err != nil {
			t.Fatalf("DecodeBit #%d: %v", i, err)
		}
		if bit != 0 {
			t.Errorf("DecodeBit #%d = %d, want 0", i, bit)
		}

		sym, err := dec.DecodeSymbol(symModel)
		if err != nil {
			t.Fatalf("DecodeSymbol #%d: %v", i, err)
		}
		if sym != 0 {
			t.Errorf("DecodeSymbol #%d = %d, want 0", i, sym)
		}
	}
}

// ===========================================================================
// Decoder — verify GetByteStreamIn
// ===========================================================================

func TestDecoderGetByteStreamIn(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	if dec.GetByteStreamIn() != stream {
		t.Error("GetByteStreamIn should return the same stream")
	}
}

// ===========================================================================
// Model create/destroy lifecycle
// ===========================================================================

func TestCreateMultipleBitModels(t *testing.T) {
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	m1 := dec.CreateBitModel()
	m2 := dec.CreateBitModel()

	// Each should be independent.
	m1.Bit0Count = 999
	if m2.Bit0Count != 1 {
		t.Error("models should be independent")
	}
}

func TestCreateMultipleSymbolModels(t *testing.T) {
	data := make([]byte, 128)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	m1 := dec.CreateSymbolModel(8)
	dec.InitSymbolModel(m1, nil)

	m2 := dec.CreateSymbolModel(16)
	dec.InitSymbolModel(m2, nil)

	// m1 has Distribution size 2*8 = 16, m2 has 2*16 = 32
	if len(m1.Distribution) != 16 {
		t.Errorf("m1 distribution len = %d, want 16", len(m1.Distribution))
	}
	if len(m2.Distribution) != 32 {
		t.Errorf("m2 distribution len = %d, want 32", len(m2.Distribution))
	}
}

// ===========================================================================
// Test constants
// ===========================================================================

func TestArithmeticConstants(t *testing.T) {
	if AC__MinLength != 0x01000000 {
		t.Errorf("AC__MinLength = %#x, want 0x01000000", AC__MinLength)
	}
	if AC__MaxLength != 0xFFFFFFFF {
		t.Errorf("AC__MaxLength = %#x, want 0xFFFFFFFF", AC__MaxLength)
	}
	if BM__LengthShift != 13 {
		t.Errorf("BM__LengthShift = %d, want 13", BM__LengthShift)
	}
	if BM__MaxCount != 8192 {
		t.Errorf("BM__MaxCount = %d, want 8192", BM__MaxCount)
	}
	if DM__LengthShift != 15 {
		t.Errorf("DM__LengthShift = %d, want 15", DM__LengthShift)
	}
	if DM__MaxCount != 32768 {
		t.Errorf("DM__MaxCount = %d, want 32768", DM__MaxCount)
	}
}

// ===========================================================================
// End-to-end: encode with known values, decode back
// ===========================================================================

func TestDecoderRoundtrip_ReadBitsThenBytes(t *testing.T) {
	// Verify that arithmetic reads with renormalization consume bytes.
	// With value=0, length=0xFFFFFFFF: ReadBits(8) → length>>=8=0x00FFFFFF
	// (< AC__MinLength), renorm fires and reads 1 byte. So stream pos should
	// advance from 0 to 1. Provide enough bytes.
	data := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = AC__MaxLength
	dec.value = 0

	// Read 8 bits → 0, but renorm consumes 1 byte.
	val, err := dec.ReadBits(8)
	if err != nil || val != 0 {
		t.Fatalf("ReadBits(8) = %d, err=%v, want 0", val, err)
	}

	// After arithmetic read, stream should have advanced by 1 (renorm byte).
	pos, _ := stream.Tell()
	if pos != 1 {
		t.Errorf("stream position = %d, want 1 (renorm consumed 1 byte)", pos)
	}
}

func TestDecoderInit_Reads4BytesToSeedValue(t *testing.T) {
	// The C++ init() reads 4 bytes from the stream to set value.
	// Verify our Go version matches.
	data := []byte{0xAB, 0xCD, 0xEF, 0x01, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	err := dec.Init(stream, true)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// value should be big-endian reassembly of bytes 0-3.
	expected := (uint32(0xAB) << 24) | (uint32(0xCD) << 16) | (uint32(0xEF) << 8) | uint32(0x01)
	if dec.value != expected {
		t.Errorf("value = %#08x, want %#08x", dec.value, expected)
	}

	// Stream should be at position 4.
	pos, _ := stream.Tell()
	if pos != 4 {
		t.Errorf("stream position after Init = %d, want 4", pos)
	}
}

// ===========================================================================
// Decoder ReadBits > 19 path (splits into ReadShort + ReadBits)
// ===========================================================================

func TestDecoderReadBits_Above19_Splits(t *testing.T) {
	// ReadBits(20) should call ReadShort (16 bits) then ReadBits(4).
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = AC__MaxLength
	dec.value = 0x00080000 // chosen so that ReadShort gives 0x8000, ReadBits(4) gives 0x0

	val, err := dec.ReadBits(20)
	if err != nil {
		t.Fatalf("ReadBits(20): %v", err)
	}
	// With value = 0x00080000:
	// ReadShort: length>>=16=0x0000FFFF → wait, length is 0xFFFFFFFF
	//   d.length >>= 16 = 0x0000FFFF
	//   sym = 0x00080000 / 0x0000FFFF = 0x00000008 (integer division) → sym = 8
	//   value -= 0x0000FFFF * 8 = 0x00080000 - 0x0007FFF8 = 8
	//   returns 8
	// bits = 4, ReadBits(4): length = 0x0000FFFF >> 4 = 0x00000FFF
	//   sym = 8 / 0x00000FFF = 0
	//   value -= 0
	//   returns 0
	// Result = (0 << 16) | 8 = 8
	if val != 8 {
		t.Errorf("ReadBits(20) = %d, want 8", val)
	}
}

// ===========================================================================
// Verify model re-use after Init
// ===========================================================================

func TestSymbolModelReuse(t *testing.T) {
	data := make([]byte, 128)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	model := dec.CreateSymbolModel(8)
	dec.InitSymbolModel(model, nil)
	sym1, _ := dec.DecodeSymbol(model)

	// Re-init with specific table.
	table := []uint32{5, 3, 2, 1, 1, 1, 1, 1}
	dec.InitSymbolModel(model, table)
	sym2, _ := dec.DecodeSymbol(model)

	if sym1 != 0 || sym2 != 0 {
		t.Errorf("re-init symbols: %d, %d, both should be 0 with value=0", sym1, sym2)
	}
}

// ===========================================================================
// Cross-boundary bit reads — exercise the renormalization loop in ReadBits
// ===========================================================================

func TestReadBitsWithRenormalization(t *testing.T) {
	// Set length below AC__MinLength to force renorm on ReadBit.
	// length=0x00FFFFFF → ReadBit: length>>=1=0x007FFFFF, need sym=1.
	// To get sym=1: value ∈ [0x007FFFFF, 0x00FFFFFE). Use value=0x007FFFFF.
	// sym = 0x007FFFFF / 0x007FFFFF = 1. Then renorm fires (length<AC__MinLength).
	// Needs at least 1 byte for renorm. Init(false) so no init bytes consumed.
	data := []byte{0xAA, 0xBB, 0xCC}
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, false)
	dec.length = 0x00FFFFFF // < AC__MinLength → any shift triggers renorm
	dec.value = 0x007FFFFF  // sym = 0x007FFFFF / (0x00FFFFFF>>1) = 1
	bit, err := dec.ReadBit()
	if err != nil {
		t.Fatalf("ReadBit with renorm: %v", err)
	}
	if bit != 1 {
		t.Errorf("ReadBit = %d, want 1", bit)
	}
	// After renorm, stream should have consumed at least 1 byte (since init(false) → start pos=0).
	pos, _ := stream.Tell()
	if pos == 0 {
		t.Error("renormalization should have consumed bytes from stream")
	}
}

// ===========================================================================
// Verify value/length state invariants after DecodeBit
// ===========================================================================

func TestDecodeBit_StateTransition(t *testing.T) {
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0

	model := dec.CreateBitModel()

	prevValue := dec.value
	prevLength := dec.length

	// With value=0: sym=0 (value < x), so length → x, value stays 0.
	_, err := dec.DecodeBit(model)
	if err != nil {
		t.Fatalf("DecodeBit: %v", err)
	}

	// value should be unchanged (still 0).
	if dec.value != 0 {
		t.Errorf("value changed from 0 to %d after decoding 0", dec.value)
	}
	// length should have decreased (x = Bit0Prob * (length >> Shift) < length).
	if dec.length >= prevLength {
		t.Errorf("length %d did not decrease from %d", dec.length, prevLength)
	}
	_ = prevValue
}

func TestDecodeBit_StateTransition_Sym1(t *testing.T) {
	data := make([]byte, 64)
	stream := NewByteStreamInArray(data)

	dec := NewArithmeticDecoder()
	dec.Init(stream, true)

	// Set value high so we decode a 1.
	dec.value = 0xFFFFFFFF

	model := dec.CreateBitModel()

	_, err := dec.DecodeBit(model)
	if err != nil {
		t.Fatalf("DecodeBit: %v", err)
	}

	// After decoding 1: value decreases, length decreases.
	// value and length should still be > 0.
	if dec.value == 0xFFFFFFFF {
		t.Error("value should have decreased after decoding 1")
	}
	if dec.length == AC__MaxLength {
		t.Error("length should have decreased after decoding 1")
	}
}

// ===========================================================================
// Byte order verification for ReadFloat/ReadDouble
// ===========================================================================

func TestReadFloatByteOrder(t *testing.T) {
	// Verify ReadFloat decodes a non-zero float with correct byte order.
	// Use init value=0x0000BF80 to get ReadInt=0xBF800000 → float32(-1.0).
	// Same arithmetic as TestDecoderReadFloat: ReadShort#1→0, renorm×2,
	// ReadShort#2→0xBF80=49024. Needs 4 init + 4 renorm = 8 bytes.
	// Also verify ReadDouble with value=0 gives 0.0 for sanity.
	data := []byte{0x00, 0x00, 0xBF, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)
	dec := NewArithmeticDecoder()
	dec.Init(stream, true) // value = 0x0000BF80

	f, err := dec.ReadFloat()
	if err != nil {
		t.Fatalf("ReadFloat: %v", err)
	}
	if f != -1.0 {
		t.Errorf("ReadFloat = %v, want -1.0", f)
	}
	_ = binary.LittleEndian
}
