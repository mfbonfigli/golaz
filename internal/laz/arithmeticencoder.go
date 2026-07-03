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
// arithmeticencoder.go — ArithmeticEncoder ported from
// src/arithmeticencoder.hpp/cpp. Implements Amir Said's FastAC entropy
// encoder. All interval updates and the Done() flush must match the C++
// bit-exactly so the produced streams decode with the existing decoder
// (and the C++ one).
//
// Structural deviation from the C++: instead of the 2x4096-byte ring buffer
// (outbuffer/manage_outbuffer), the whole Init→Done production accumulates
// in a growing []byte. Carry propagation walks that slice backwards and
// Done() flushes it in one PutBytes call. The byte output is identical —
// chunks are at most tens of KB so memory is not a concern, and the ring
// was a known bug source (carry reach across the wrap).
package laz

import (
	"fmt"
	"math"
)

// ArithmeticEncoder encodes bits and symbols into an entropy-coded byte
// stream using adaptive probability models (ArithmeticBitModel and
// ArithmeticModel). The production is buffered internally and flushed to
// the bound ByteStreamOut in Done(). After Done() the encoder can be
// re-Init()-ed for the next chunk.
type ArithmeticEncoder struct {
	outstream ByteStreamOut
	outbuffer []byte // whole Init→Done production (C++: 2x4096 ring buffer)
	base      uint32 // current interval base (C++: base)
	length    uint32 // current interval length (C++: length)
}

// NewArithmeticEncoder creates a new ArithmeticEncoder.
func NewArithmeticEncoder() *ArithmeticEncoder {
	return &ArithmeticEncoder{}
}

// Init binds the encoder to an output stream and resets the coding state.
// Unlike the decoder there is no 4-byte preload: the decoder's seed comes
// from the encoder's flush in Done().
func (e *ArithmeticEncoder) Init(outstream ByteStreamOut) error {
	if outstream == nil {
		return fmt.Errorf("init: nil outstream")
	}
	e.outstream = outstream
	e.base = 0
	e.length = AC__MaxLength
	e.outbuffer = e.outbuffer[:0]
	return nil
}

// Done finalizes the encoding (C++ ArithmeticEncoder::done()): it emits the
// final interval bytes, appends the two-or-three zero tail bytes that keep
// the decoder's byte reads in sync, and flushes the whole buffered
// production to the output stream. It returns the number of bytes written.
// The encoder is disassociated from the stream and can be Init()-ed again
// for the next chunk.
func (e *ArithmeticEncoder) Done() (int, error) {
	if e.outstream == nil {
		return 0, fmt.Errorf("done: no outstream")
	}

	initBase := e.base // done encoding: set final data bytes
	anotherByte := true

	if e.length > 2*AC__MinLength {
		e.base += AC__MinLength       // base offset
		e.length = AC__MinLength >> 1 // set new length for 1 more byte
	} else {
		e.base += AC__MinLength >> 1  // base offset
		e.length = AC__MinLength >> 9 // set new length for 2 more bytes
		anotherByte = false
	}

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	e.renormEncInterval() // renormalization = output last bytes

	// Write two or three zero bytes to be in sync with the decoder's byte reads.
	e.outbuffer = append(e.outbuffer, 0, 0)
	if anotherByte {
		e.outbuffer = append(e.outbuffer, 0)
	}

	n := len(e.outbuffer)
	if n > 0 {
		if err := e.outstream.PutBytes(e.outbuffer); err != nil {
			return 0, fmt.Errorf("done: flush: %w", err)
		}
	}

	e.outstream = nil
	return n, nil
}

// GetByteStreamOut returns the underlying ByteStreamOut. Used when the
// ArithmeticEncoder acts as a dummy (just passthrough writes).
func (e *ArithmeticEncoder) GetByteStreamOut() ByteStreamOut {
	return e.outstream
}

// ---------------------------------------------------------------------------
// Model lifecycle
// ---------------------------------------------------------------------------

// CreateBitModel creates a new initialized ArithmeticBitModel.
func (e *ArithmeticEncoder) CreateBitModel() *ArithmeticBitModel {
	return NewArithmeticBitModel()
}

// InitBitModel re-initializes an existing bit model.
func (e *ArithmeticEncoder) InitBitModel(m *ArithmeticBitModel) {
	m.Init()
}

// CreateSymbolModel creates a new encoder-mode ArithmeticModel for n symbols
// (no decoder table is allocated or built).
func (e *ArithmeticEncoder) CreateSymbolModel(n uint32) *ArithmeticModel {
	return NewArithmeticModel(n, true)
}

// InitSymbolModel initializes a symbol model, optionally with a frequency table.
func (e *ArithmeticEncoder) InitSymbolModel(m *ArithmeticModel, table []uint32) {
	m.Init(table)
}

// ---------------------------------------------------------------------------
// carry propagation and renormalization (private)
// ---------------------------------------------------------------------------

// propagateCarry propagates an interval-base overflow into the already
// produced bytes: walking the buffer backwards, 0xFF bytes become 0x00 and
// the first non-0xFF byte is incremented (C++ propagate_carry, adapted from
// the ring buffer to the flat slice).
func (e *ArithmeticEncoder) propagateCarry() {
	p := len(e.outbuffer) - 1
	for p >= 0 && e.outbuffer[p] == 0xFF {
		e.outbuffer[p] = 0
		p--
	}
	if p < 0 {
		// Cannot happen for a valid encoder state: the interval invariant
		// guarantees no carry before the first byte is produced (the C++
		// only asserts this).
		panic("ArithmeticEncoder: carry propagated past start of buffer")
	}
	e.outbuffer[p]++
}

// renormEncInterval performs the renormalization loop exactly as in C++:
//
//	do {
//	  emit byte base>>24;
//	  base <<= 8;
//	} while ((length <<= 8) < AC__MinLength);
func (e *ArithmeticEncoder) renormEncInterval() {
	for {
		// Output and discard top byte.
		e.outbuffer = append(e.outbuffer, byte(e.base>>24))
		e.base <<= 8
		e.length <<= 8 // length multiplied by 256
		if e.length >= AC__MinLength {
			break
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeBit — encode a single bit with an adaptive bit model
// ---------------------------------------------------------------------------

// EncodeBit encodes a single bit (0 or 1) using the given ArithmeticBitModel.
// This must match C++ ArithmeticEncoder::encodeBit() exactly.
func (e *ArithmeticEncoder) EncodeBit(m *ArithmeticBitModel, sym uint32) {
	// product l * p0
	x := m.Bit0Prob * (e.length >> BM__LengthShift)

	// Update interval.
	if sym == 0 {
		e.length = x
		m.Bit0Count++
	} else {
		initBase := e.base
		e.base += x
		e.length -= x
		if initBase > e.base {
			e.propagateCarry() // overflow = carry
		}
	}

	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}

	m.BitsUntilUpdate--
	if m.BitsUntilUpdate == 0 {
		m.Update() // periodic model update
	}
}

// ---------------------------------------------------------------------------
// EncodeSymbol — encode an N-ary symbol with an adaptive symbol model
// ---------------------------------------------------------------------------

// EncodeSymbol encodes a symbol using the given ArithmeticModel.
// This must match C++ ArithmeticEncoder::encodeSymbol() exactly — note the
// asymmetry: the last-symbol case shifts length non-destructively, while
// the general case destructively shifts length before both products.
func (e *ArithmeticEncoder) EncodeSymbol(m *ArithmeticModel, sym uint32) {
	var x uint32
	initBase := e.base

	// Compute products.
	if sym == m.LastSymbol {
		x = m.Distribution[sym] * (e.length >> DM__LengthShift)
		e.base += x   // update interval
		e.length -= x // no product needed
	} else {
		e.length >>= DM__LengthShift
		x = m.Distribution[sym] * e.length
		e.base += x // update interval
		e.length = m.Distribution[sym+1]*e.length - x
	}

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}

	m.SymbolCount[sym]++
	m.SymbolsUntilUpdate--
	if m.SymbolsUntilUpdate == 0 {
		m.update() // periodic model update
	}
}

// ---------------------------------------------------------------------------
// WriteBit, WriteBits, WriteByte, WriteShort, WriteInt, WriteFloat,
// WriteInt64, WriteDouble — uniform encoding without modelling
// ---------------------------------------------------------------------------

// WriteBit encodes a single bit without modelling.
func (e *ArithmeticEncoder) WriteBit(sym uint32) {
	initBase := e.base
	e.length >>= 1
	e.base += sym * e.length // new interval base and length

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}
}

// WriteBits encodes 'bits' bits (1..32) without modelling.
func (e *ArithmeticEncoder) WriteBits(bits, sym uint32) {
	if bits > 19 {
		e.WriteShort(uint16(sym & 0xFFFF))
		sym >>= 16
		bits -= 16
	}

	initBase := e.base
	e.length >>= bits
	e.base += sym * e.length // new interval base and length

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}
}

// WriteByte encodes an unsigned 8-bit value without modelling.
func (e *ArithmeticEncoder) WriteByte(sym byte) {
	initBase := e.base
	e.length >>= 8
	e.base += uint32(sym) * e.length // new interval base and length

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}
}

// WriteShort encodes an unsigned 16-bit value without modelling.
func (e *ArithmeticEncoder) WriteShort(sym uint16) {
	initBase := e.base
	e.length >>= 16
	e.base += uint32(sym) * e.length // new interval base and length

	if initBase > e.base {
		e.propagateCarry() // overflow = carry
	}
	if e.length < AC__MinLength {
		e.renormEncInterval() // renormalization
	}
}

// WriteInt encodes an unsigned 32-bit value without modelling
// (lower 16 bits first, then upper 16 bits, mirroring the decoder).
func (e *ArithmeticEncoder) WriteInt(sym uint32) {
	e.WriteShort(uint16(sym & 0xFFFF)) // lower 16 bits
	e.WriteShort(uint16(sym >> 16))    // upper 16 bits
}

// WriteFloat encodes a 32-bit float without modelling
// (reinterprets the IEEE 754 float32 bits as uint32).
func (e *ArithmeticEncoder) WriteFloat(sym float32) {
	e.WriteInt(math.Float32bits(sym))
}

// WriteInt64 encodes an unsigned 64-bit value without modelling
// (lower 32 bits first, then upper 32 bits, mirroring the decoder).
func (e *ArithmeticEncoder) WriteInt64(sym uint64) {
	e.WriteInt(uint32(sym & 0xFFFFFFFF)) // lower 32 bits
	e.WriteInt(uint32(sym >> 32))        // upper 32 bits
}

// WriteDouble encodes a 64-bit float without modelling
// (reinterprets the IEEE 754 float64 bits as uint64).
func (e *ArithmeticEncoder) WriteDouble(sym float64) {
	e.WriteInt64(math.Float64bits(sym))
}
