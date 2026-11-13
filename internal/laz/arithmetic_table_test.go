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
	"math/rand"
	"testing"
)

// golaz sizes the decoder look-up table differently from the C++ reference
// (finer, and present for small alphabets). These tests pin down why that is
// safe: the table is only a search accelerator, so decoding is identical to
// the table-free reference search for every possible input.

var tableTestAlphabets = []uint32{2, 3, 4, 5, 8, 13, 16, 17, 32, 33, 64, 128, 256, 515, 516, 1024, 2048}

// maxDecoderValue is the largest value/(length>>15) a decoder can produce:
// value < length, and length >= 2^24 is not a multiple of 2^15 in general.
const maxDecoderValue = (1 << DM__LengthShift) + 63

func TestDecoderTable_Sizing(t *testing.T) {
	for _, symbols := range tableTestAlphabets {
		m := NewArithmeticModel(symbols, false)
		m.Init(nil)
		if m.DecoderTable == nil {
			t.Errorf("%d symbols: no decoder table", symbols)
			continue
		}
		if m.TableShift < 6 {
			t.Errorf("%d symbols: table shift %d < 6: a decoder value of %d would index past the table",
				symbols, m.TableShift, maxDecoderValue)
		}
		if m.TableSize != 1<<(DM__LengthShift-m.TableShift) {
			t.Errorf("%d symbols: table size %d does not match shift %d", symbols, m.TableSize, m.TableShift)
		}
		if got, want := uint32(len(m.DecoderTable)), m.TableSize+2; got != want {
			t.Errorf("%d symbols: table has %d entries, want %d", symbols, got, want)
		}
		if maxDecoderValue>>m.TableShift+1 >= uint32(len(m.DecoderTable)) {
			t.Errorf("%d symbols: the largest decoder value indexes past the table", symbols)
		}
	}

	// Encoder-side models never carry a table.
	if m := NewArithmeticModel(64, true); func() bool { m.Init(nil); return m.DecoderTable != nil }() {
		t.Error("encoder model has a decoder table")
	}
}

// adaptRandomly feeds a model a skewed random symbol history, the way decoding
// does, so its distribution and table go through many updates.
func adaptRandomly(m *ArithmeticModel, rng *rand.Rand, n int) {
	for i := 0; i < n; i++ {
		// squaring skews towards low symbols: some very likely, most rare
		f := rng.Float64()
		sym := uint32(f * f * float64(m.Symbols))
		m.SymbolCount[sym]++
		m.SymbolsUntilUpdate--
		if m.SymbolsUntilUpdate == 0 {
			m.update()
		}
	}
}

// For every decoder value, the table look-up followed by the bisection of
// DecodeSymbol lands on the symbol a plain linear search finds.
func TestDecoderTable_LookupFindsTheSymbol(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, symbols := range tableTestAlphabets {
		m := NewArithmeticModel(symbols, false)
		m.Init(nil)
		for round := 0; round < 6; round++ {
			for dv := uint32(0); dv <= maxDecoderValue; dv++ {
				// the reference answer: last symbol whose cumulative
				// probability does not exceed dv
				want := uint32(0)
				for k := uint32(1); k < symbols && m.Distribution[k] <= dv; k++ {
					want = k
				}

				tt := dv >> m.TableShift
				sym := m.DecoderTable[tt]
				n := m.DecoderTable[tt+1] + 1
				for n > sym+1 {
					k := (sym + n) >> 1
					if m.Distribution[k] > dv {
						n = k
					} else {
						sym = k
					}
				}
				if sym != want {
					t.Fatalf("%d symbols, round %d, dv %d: table search gives %d, linear search %d",
						symbols, round, dv, sym, want)
				}
			}
			adaptRandomly(m, rng, 500+int(symbols)*20)
		}
	}
}

// Decoding arbitrary bytes gives the same symbols and leaves the decoder in
// the same state with the table as with the table-free search of the C++
// reference (its path for small alphabets), for every alphabet size.
func TestDecodeSymbol_TableMatchesTableFreeSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, symbols := range tableTestAlphabets {
		for trial := 0; trial < 4; trial++ {
			data := make([]byte, 6000)
			switch trial {
			case 0:
				rng.Read(data)
			case 1: // all ones: value == length from the start, which no
				// encoder produces and which never recovers (corrupt input)
				for i := range data {
					data[i] = 0xFF
				}
			case 2: // all zeros: always the first symbol
			case 3: // long runs
				for i := range data {
					data[i] = byte(i / 97)
				}
			}

			withTable := NewArithmeticModel(symbols, false)
			withTable.Init(nil)
			tableFree := NewArithmeticModel(symbols, false)
			tableFree.Init(nil)
			// Dropping the table forces the multiplication-only search, and
			// update() keeps it that way; the distribution is untouched.
			tableFree.DecoderTable = nil

			decA, decB := NewArithmeticDecoder(), NewArithmeticDecoder()
			if err := decA.Init(NewByteStreamInArray(data), true); err != nil {
				t.Fatal(err)
			}
			if err := decB.Init(NewByteStreamInArray(data), true); err != nil {
				t.Fatal(err)
			}
			// Highly predictable streams (all zeros) cost almost no bytes per
			// symbol, so cap the number of steps rather than wait for the end.
			for i := 0; i < 30000; i++ {
				symA, errA := decA.DecodeSymbol(withTable)
				symB, errB := decB.DecodeSymbol(tableFree)
				if (errA == nil) != (errB == nil) {
					t.Fatalf("%d symbols, trial %d, step %d: errors differ: %v vs %v", symbols, trial, i, errA, errB)
				}
				if errA != nil {
					if i < 1000 {
						t.Fatalf("%d symbols, trial %d: stream ended after only %d symbols", symbols, trial, i)
					}
					break
				}
				if symA != symB || decA.Value() != decB.Value() || decA.Length() != decB.Length() {
					t.Fatalf("%d symbols, trial %d, step %d: table %d (value %#x length %#x), table-free %d (value %#x length %#x)",
						symbols, trial, i, symA, decA.Value(), decA.Length(), symB, decB.Value(), decB.Length())
				}
			}
		}
	}
}

// The decoder reads in-memory streams directly. That must be invisible: same
// symbols, same stream position and same end-of-stream error as through the
// ByteStreamIn interface.
func TestDecodeSymbol_DirectMemoryAccessMatchesInterface(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	data := make([]byte, 3000)
	rng.Read(data)

	mem := NewByteStreamInArray(data)
	// Same bytes behind a type the decoder does not special-case.
	plain := struct{ ByteStreamIn }{NewByteStreamInArray(data)}

	decMem, decPlain := NewArithmeticDecoder(), NewArithmeticDecoder()
	if err := decMem.Init(mem, true); err != nil {
		t.Fatal(err)
	}
	if err := decPlain.Init(plain, true); err != nil {
		t.Fatal(err)
	}
	if decMem.mem == nil || decPlain.mem != nil {
		t.Fatal("test setup: expected exactly the first decoder to use direct memory access")
	}
	mA := NewArithmeticModel(256, false)
	mA.Init(nil)
	mB := NewArithmeticModel(256, false)
	mB.Init(nil)
	bitA, bitB := NewArithmeticBitModel(), NewArithmeticBitModel()

	for i := 0; ; i++ {
		var a, b uint32
		var errA, errB error
		switch i % 3 {
		case 0:
			a, errA = decMem.DecodeSymbol(mA)
			b, errB = decPlain.DecodeSymbol(mB)
		case 1:
			a, errA = decMem.DecodeBit(bitA)
			b, errB = decPlain.DecodeBit(bitB)
		case 2:
			a, errA = decMem.ReadBits(11)
			b, errB = decPlain.ReadBits(11)
		}
		if (errA == nil) != (errB == nil) {
			t.Fatalf("step %d: errors differ: %v vs %v", i, errA, errB)
		}
		posA, _ := mem.Tell()
		posB, _ := plain.Tell()
		if errA != nil {
			if errA.Error() != errB.Error() {
				t.Errorf("end-of-stream errors differ: %q vs %q", errA, errB)
			}
			if posA != int64(len(data)) || posB != int64(len(data)) {
				t.Errorf("stream positions at end: %d and %d, want %d", posA, posB, len(data))
			}
			if i < 1000 {
				t.Fatalf("stream ended after only %d steps", i)
			}
			return
		}
		if a != b || posA != posB || decMem.Value() != decPlain.Value() || decMem.Length() != decPlain.Length() {
			t.Fatalf("step %d: direct %d at byte %d, interface %d at byte %d", i, a, posA, b, posB)
		}
	}
}

// Running out of input is an error, but it must leave the decoder usable: a
// caller that keeps decoding gets errors or garbage, never a division by zero
// (the interval length used to be left below the renormalization threshold).
func TestDecoder_UsableAfterEndOfStream(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	data := make([]byte, 64)
	rng.Read(data)
	for _, viaInterface := range []bool{false, true} {
		var stream ByteStreamIn = NewByteStreamInArray(data)
		if viaInterface {
			stream = struct{ ByteStreamIn }{stream}
		}
		dec := NewArithmeticDecoder()
		if err := dec.Init(stream, true); err != nil {
			t.Fatal(err)
		}
		m := NewArithmeticModel(256, false)
		m.Init(nil)
		small := NewArithmeticModel(4, false)
		small.Init(nil)
		bit := NewArithmeticBitModel()

		sawEOF := false
		for i := 0; i < 2000; i++ {
			var err error
			switch i % 4 {
			case 0:
				_, err = dec.DecodeSymbol(m)
			case 1:
				_, err = dec.DecodeSymbol(small)
			case 2:
				_, err = dec.DecodeBit(bit)
			case 3:
				_, err = dec.ReadBits(13)
			}
			if err != nil {
				sawEOF = true
			}
			if dec.Length() < AC__MinLength {
				t.Fatalf("interface=%v step %d: interval length %#x below the renormalization threshold", viaInterface, i, dec.Length())
			}
		}
		if !sawEOF {
			t.Fatalf("interface=%v: the stream never ran out", viaInterface)
		}
	}
}

// FuzzDecodeSymbol_TableMatchesTableFreeSearch is the fuzzing form of
// TestDecodeSymbol_TableMatchesTableFreeSearch: for any bytes and any alphabet
// size, decoding with the look-up table must agree, symbol by symbol and in
// decoder state, with the table-free search. Run it with
//
//	go test ./internal/laz -run '^$' -fuzz FuzzDecodeSymbol_TableMatchesTableFreeSearch -fuzztime 1m
func FuzzDecodeSymbol_TableMatchesTableFreeSearch(f *testing.F) {
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x02, 0x03, 0x04}, uint16(256))
	f.Add(make([]byte, 64), uint16(5))
	f.Add([]byte("an arbitrary compressed stream, as far as the decoder knows"), uint16(2048))
	f.Fuzz(func(t *testing.T, data []byte, alphabet uint16) {
		symbols := uint32(alphabet)%2047 + 2 // 2..2048
		withTable := NewArithmeticModel(symbols, false)
		withTable.Init(nil)
		tableFree := NewArithmeticModel(symbols, false)
		tableFree.Init(nil)
		tableFree.DecoderTable = nil

		decA, decB := NewArithmeticDecoder(), NewArithmeticDecoder()
		errA := decA.Init(NewByteStreamInArray(data), true)
		errB := decB.Init(NewByteStreamInArray(data), true)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("Init errors differ: %v vs %v", errA, errB)
		}
		if errA != nil {
			return
		}
		for i := 0; i < 20000; i++ {
			symA, errA := decA.DecodeSymbol(withTable)
			symB, errB := decB.DecodeSymbol(tableFree)
			if (errA == nil) != (errB == nil) {
				t.Fatalf("step %d: errors differ: %v vs %v", i, errA, errB)
			}
			if errA != nil {
				return
			}
			if symA != symB || decA.Value() != decB.Value() || decA.Length() != decB.Length() {
				t.Fatalf("%d symbols, step %d: table %d (value %#x length %#x), table-free %d (value %#x length %#x)",
					symbols, i, symA, decA.Value(), decA.Length(), symB, decB.Value(), decB.Length())
			}
		}
	})
}
