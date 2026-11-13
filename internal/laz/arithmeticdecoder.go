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
// arithmeticdecoder.go — ArithmeticDecoder ported from
// src/arithmeticdecoder.hpp/cpp. Implements Amir Said's FastAC entropy decoder.
// The renormalization loop and all decode methods must match C++ bit-exactly.
package laz

import (
	"fmt"
	"io"
	"math"
	"os"
)

// ArithmeticDecoder decodes entropy-coded bits and symbols from a ByteStreamIn
// using adaptive probability models (ArithmeticBitModel and ArithmeticModel).
type ArithmeticDecoder struct {
	instream ByteStreamIn
	value    uint32 // current decoder value (C++: value)
	length   uint32 // current interval length (C++: length)
	Debug    bool   // when true, prints value/length transitions to stderr

	// mem is instream when that is an in-memory stream, nil otherwise. The
	// decoder then takes the next byte straight from the stream's slice
	// instead of making an interface call per byte. It advances the stream's
	// own position as it goes, so both views of the stream always agree.
	mem *ByteStreamInArray
}

// NewArithmeticDecoder creates a new ArithmeticDecoder.
func NewArithmeticDecoder() *ArithmeticDecoder {
	return &ArithmeticDecoder{}
}

// Value returns the current decoder state (for debug/test inspection).
func (d *ArithmeticDecoder) Value() uint32 { return d.value }

// Length returns the current interval length (for debug/test inspection).
func (d *ArithmeticDecoder) Length() uint32 { return d.length }

// Init binds the decoder to an input stream. If reallyInit is true, reads
// the initial 4 bytes to seed the decoder value.
func (d *ArithmeticDecoder) Init(instream ByteStreamIn, reallyInit bool) error {
	if instream == nil {
		return fmt.Errorf("init: nil instream")
	}
	d.instream = instream
	d.mem, _ = instream.(*ByteStreamInArray)
	d.length = AC__MaxLength
	if reallyInit {
		b0, err := instream.GetByte()
		if err != nil {
			return fmt.Errorf("init: read byte 0: %w", err)
		}
		b1, err := instream.GetByte()
		if err != nil {
			return fmt.Errorf("init: read byte 1: %w", err)
		}
		b2, err := instream.GetByte()
		if err != nil {
			return fmt.Errorf("init: read byte 2: %w", err)
		}
		b3, err := instream.GetByte()
		if err != nil {
			return fmt.Errorf("init: read byte 3: %w", err)
		}
		d.value = (uint32(b0) << 24) | (uint32(b1) << 16) | (uint32(b2) << 8) | uint32(b3)
	}
	return nil
}

// Done disassociates the decoder from the stream.
func (d *ArithmeticDecoder) Done() {
	d.instream = nil
	d.mem = nil
}

// GetByteStreamIn returns the underlying ByteStreamIn. Used when the
// ArithmeticDecoder acts as a dummy (just passthrough reads).
func (d *ArithmeticDecoder) GetByteStreamIn() ByteStreamIn {
	return d.instream
}

// ---------------------------------------------------------------------------
// Model lifecycle
// ---------------------------------------------------------------------------

// CreateBitModel creates a new initialized ArithmeticBitModel.
func (d *ArithmeticDecoder) CreateBitModel() *ArithmeticBitModel {
	return NewArithmeticBitModel()
}

// InitBitModel re-initializes an existing bit model.
func (d *ArithmeticDecoder) InitBitModel(m *ArithmeticBitModel) {
	m.Init()
}

// CreateSymbolModel creates a new ArithmeticModel for n symbols.
func (d *ArithmeticDecoder) CreateSymbolModel(n uint32) *ArithmeticModel {
	return NewArithmeticModel(n, false)
}

// InitSymbolModel initializes a symbol model, optionally with a frequency table.
func (d *ArithmeticDecoder) InitSymbolModel(m *ArithmeticModel, table []uint32) {
	m.Init(table)
}

// ---------------------------------------------------------------------------
// debugTracer — prints value/length transitions
// ---------------------------------------------------------------------------

func (d *ArithmeticDecoder) trace(funcName string, sym uint32) {
	if !d.Debug {
		return
	}
	fmt.Fprintf(os.Stderr, "[DECODER] %s sym=%d value=%#08x length=%#08x\n",
		funcName, sym, d.value, d.length)
}

// ---------------------------------------------------------------------------
// renormalization (private) — exact port of renorm_dec_interval()
// ---------------------------------------------------------------------------

// renormDecInterval performs the renormalization loop exactly as in C++:
//
//	do {
//	  value = (value << 8) | instream.getByte();
//	} while ((length <<= 8) < AC__MinLength);
func (d *ArithmeticDecoder) renormDecInterval() error {
	n := 0
	for {
		if d.Debug {
			n++
			fmt.Fprintf(os.Stderr, "[DECODER] renorm_iter=%d before: value=%#08x length=%#08x\n",
				n, d.value, d.length)
		}
		var b byte
		if mem := d.mem; mem != nil && mem.pos < int64(len(mem.data)) {
			b = mem.data[mem.pos]
			mem.pos++
		} else {
			var err error
			if b, err = d.instream.GetByte(); err != nil {
				d.value, d.length = padRenorm(d.value, d.length)
				return fmt.Errorf("renorm: %w", err)
			}
		}
		d.value = (d.value << 8) | uint32(b)
		d.length <<= 8
		if d.Debug {
			fmt.Fprintf(os.Stderr, "[DECODER] renorm_iter=%d after:  value=%#08x length=%#08x (byte=%#02x)\n",
				n, d.value, d.length, b)
		}
		if d.length >= AC__MinLength {
			break
		}
	}
	return nil
}

// padRenorm completes a renormalization that ran out of input by shifting in
// zero bytes. The stream is over and the caller gets the end-of-file error
// either way; this only restores length >= AC__MinLength, so that a caller
// who keeps decoding after the error gets garbage rather than a division by
// zero (the decode functions divide by length >> 15).
func padRenorm(value, length uint32) (uint32, uint32) {
	for length < AC__MinLength {
		value <<= 8
		length <<= 8
	}
	return value, length
}

// ---------------------------------------------------------------------------
// DecodeBit — decode a single bit with an adaptive bit model
// ---------------------------------------------------------------------------

// DecodeBit decodes a single bit using the given ArithmeticBitModel.
// Returns 0 or 1. This must match C++ ArithmeticDecoder::decodeBit() exactly.
func (d *ArithmeticDecoder) DecodeBit(m *ArithmeticBitModel) (uint32, error) {
	// product l * p0
	x := m.Bit0Prob * (d.length >> BM__LengthShift)
	sym := uint32(0)
	if d.value >= x {
		sym = 1
	}

	if sym == 0 {
		d.length = x
		m.Bit0Count++
	} else {
		d.value -= x
		d.length -= x
	}

	if d.length < AC__MinLength {
		if err := d.renormDecInterval(); err != nil {
			return 0, err
		}
	}

	m.BitsUntilUpdate--
	if m.BitsUntilUpdate == 0 {
		m.Update()
	}

	d.trace("DecodeBit", sym)
	return sym, nil
}

// ---------------------------------------------------------------------------
// DecodeSymbol — decode an N-ary symbol with an adaptive symbol model
// ---------------------------------------------------------------------------

// DecodeSymbol decodes a symbol from the given ArithmeticModel.
// Returns the symbol index. This must match C++ ArithmeticDecoder::decodeSymbol() exactly.
func (d *ArithmeticDecoder) DecodeSymbol(m *ArithmeticModel) (uint32, error) {
	// This is the hottest function of the decoder: nearly two thirds of the
	// time spent reading a LAZ file. value and length are kept in locals for
	// the whole call, because the compiler must assume that a store through m
	// can alias d and would otherwise reload them after each one.
	var sym, x uint32
	value, length := d.value, d.length
	y := length
	dist := m.Distribution
	length >>= DM__LengthShift

	if table := m.DecoderTable; table != nil {
		// Use table look-up for faster decoding.
		dv := value / length
		// On a well-formed stream value < length, so dv is at most 2^15+63
		// and t at most TableSize. A corrupt stream can break that (a chunk
		// starting with FF FF FF FF sets value == length for good); the
		// clamp then selects the last symbol, exactly as the table-free
		// search below does, instead of indexing past the table (the C++
		// reference reads out of bounds here).
		t := min(dv>>m.TableShift, m.TableSize)

		sym = table[t] // initial decision based on table look-up
		n := table[t+1] + 1

		for n > sym+1 { // finish with bisection search
			k := (sym + n) >> 1
			if dist[k] > dv {
				n = k
			} else {
				sym = k
			}
		}

		// Compute products.
		x = dist[sym] * length
		if sym != m.LastSymbol {
			y = dist[sym+1] * length
		}
	} else {
		// Decode using only multiplications (bisection search).
		n := m.Symbols
		k := n >> 1

		for {
			z := length * dist[k]
			if z > value {
				n = k
				y = z // value is smaller
			} else {
				sym = k
				x = z // value is larger or equal
			}
			nextK := (sym + n) >> 1
			if nextK == sym {
				break
			}
			k = nextK
		}
	}

	value -= x // update interval
	length = y - x

	if length < AC__MinLength {
		if mem := d.mem; mem != nil && !d.Debug {
			// renormDecInterval, inlined for in-memory streams.
			data, pos := mem.data, mem.pos
			for {
				if pos >= int64(len(data)) {
					mem.pos = pos
					d.value, d.length = padRenorm(value, length)
					return 0, fmt.Errorf("renorm: getByte: %w", io.EOF)
				}
				value = (value << 8) | uint32(data[pos])
				pos++
				length <<= 8
				if length >= AC__MinLength {
					break
				}
			}
			mem.pos = pos
			d.value, d.length = value, length
		} else {
			d.value, d.length = value, length
			if err := d.renormDecInterval(); err != nil {
				return 0, err
			}
		}
	} else {
		d.value, d.length = value, length
	}

	m.SymbolCount[sym]++
	m.SymbolsUntilUpdate--
	if m.SymbolsUntilUpdate == 0 {
		m.update()
	}

	if d.Debug {
		d.trace("DecodeSymbol", sym)
	}
	return sym, nil
}

// ---------------------------------------------------------------------------
// ReadBit, ReadBits, ReadByte, ReadShort, ReadInt, ReadFloat, ReadInt64, ReadDouble
// — uniform decoding without modelling
// ---------------------------------------------------------------------------

// ReadBit decodes a single bit without modelling.
func (d *ArithmeticDecoder) ReadBit() (uint32, error) {
	d.trace("ReadBit_before", 0)

	d.length >>= 1
	sym := d.value / d.length // decode symbol, change length
	d.value -= d.length * sym // update interval

	if d.Debug {
		fmt.Fprintf(os.Stderr, "[DECODER] ReadBit: length>>=1=%#08x sym=%d value_after=%#08x\n",
			d.length, sym, d.value)
	}

	if d.length < AC__MinLength {
		if err := d.renormDecInterval(); err != nil {
			return 0, err
		}
	}

	if sym >= 2 {
		// C++ arithmeticdecoder.cpp throws 4711 here: only a corrupt stream
		// can push the decoded symbol out of range.
		return 0, fmt.Errorf("readBit: decoded symbol %d out of range (corrupt stream)", sym)
	}
	return sym, nil
}

// ReadBits decodes 'bits' bits without modelling.
// Out-of-range symbols indicate a corrupt stream and return an error,
// matching the integrity checks in C++ arithmeticdecoder.cpp.
func (d *ArithmeticDecoder) ReadBits(bits uint32) (uint32, error) {
	if bits == 0 || bits > 32 {
		return 0, fmt.Errorf("readBits: invalid bit count %d", bits)
	}

	if bits > 19 {
		if d.Debug {
			fmt.Fprintf(os.Stderr, "[DECODER] ReadBits(%d): splitting into ReadShort + ReadBits(%d)\n", bits, bits-16)
		}
		tmp, err := d.ReadShort()
		if err != nil {
			return 0, err
		}
		bits -= 16
		tmp1, err := d.ReadBits(bits)
		if err != nil {
			return 0, err
		}
		result := (tmp1 << 16) | uint32(tmp)
		if d.Debug {
			fmt.Fprintf(os.Stderr, "[DECODER] ReadBits: result=%#08x (hi=%#04x lo=%#04x)\n", result, tmp1, tmp)
		}
		return result, nil
	}

	if d.Debug {
		d.trace(fmt.Sprintf("ReadBits(%d)_before", bits), 0)
	}

	d.length >>= bits
	sym := d.value / d.length // decode symbol, change length
	d.value -= d.length * sym // update interval

	if d.Debug {
		fmt.Fprintf(os.Stderr, "[DECODER] ReadBits(%d): length>>=%d=%#08x sym=%d value_after=%#08x\n",
			bits, bits, d.length, sym, d.value)
	}

	if d.length < AC__MinLength {
		if err := d.renormDecInterval(); err != nil {
			return 0, err
		}
	}

	if sym >= uint32(1)<<bits {
		return 0, fmt.Errorf("readBits(%d): decoded symbol %d out of range (corrupt stream)", bits, sym)
	}
	return sym, nil
}

// ReadByte decodes an unsigned 8-bit value without modelling.
func (d *ArithmeticDecoder) ReadByte() (byte, error) {
	d.trace("ReadByte_before", 0)

	d.length >>= 8
	sym := d.value / d.length
	d.value -= d.length * sym

	if d.Debug {
		fmt.Fprintf(os.Stderr, "[DECODER] ReadByte: length>>=8=%#08x sym=%d value_after=%#08x\n",
			d.length, sym, d.value)
	}

	if d.length < AC__MinLength {
		if err := d.renormDecInterval(); err != nil {
			return 0, err
		}
	}

	if sym >= 1<<8 {
		return 0, fmt.Errorf("readByte: decoded symbol %d out of range (corrupt stream)", sym)
	}
	return byte(sym), nil
}

// ReadShort decodes an unsigned 16-bit value without modelling.
func (d *ArithmeticDecoder) ReadShort() (uint16, error) {
	d.trace("ReadShort_before", 0)

	d.length >>= 16
	sym := d.value / d.length
	d.value -= d.length * sym

	if d.Debug {
		fmt.Fprintf(os.Stderr, "[DECODER] ReadShort: length>>=16=%#08x sym=%d value_after=%#08x\n",
			d.length, sym, d.value)
	}

	if d.length < AC__MinLength {
		if err := d.renormDecInterval(); err != nil {
			return 0, err
		}
	}

	if sym >= 1<<16 {
		return 0, fmt.Errorf("readShort: decoded symbol %d out of range (corrupt stream)", sym)
	}
	return uint16(sym), nil
}

// ReadInt decodes an unsigned 32-bit value without modelling.
func (d *ArithmeticDecoder) ReadInt() (uint32, error) {
	lower, err := d.ReadShort()
	if err != nil {
		return 0, err
	}
	upper, err := d.ReadShort()
	if err != nil {
		return 0, err
	}
	return (uint32(upper) << 16) | uint32(lower), nil
}

// ReadFloat decodes a 32-bit float without modelling
// (reinterprets the decoded uint32 as IEEE 754 float32).
func (d *ArithmeticDecoder) ReadFloat() (float32, error) {
	u, err := d.ReadInt()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(u), nil
}

// ReadInt64 decodes an unsigned 64-bit value without modelling.
func (d *ArithmeticDecoder) ReadInt64() (uint64, error) {
	lower, err := d.ReadInt()
	if err != nil {
		return 0, err
	}
	upper, err := d.ReadInt()
	if err != nil {
		return 0, err
	}
	return (uint64(upper) << 32) | uint64(lower), nil
}

// ReadDouble decodes a 64-bit float without modelling
// (reinterprets the decoded uint64 as IEEE 754 float64).
func (d *ArithmeticDecoder) ReadDouble() (float64, error) {
	u, err := d.ReadInt64()
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(u), nil
}
