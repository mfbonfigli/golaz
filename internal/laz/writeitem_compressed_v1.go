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

// writeitem_compressed_v1.go — the WAVEPACKET13 v1 compressed writer, ported
// from src/laswriteitemcompressed_v1.cpp (LASwriteItemCompressed_WAVEPACKET13_v1
// only). WAVEPACKET13 items are ALWAYS compressed with version 1, even in
// otherwise-v2 configurations (point formats 4/5), so this single v1 writer
// is required for pf4/5 support. The other v1 writers are intentionally not
// ported (nothing emits v1 items unless explicitly requested).
package laz

import (
	"math"
)

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// WAVEPACKET13 v1 — mirror of LASreadItemCompressedWavepacket13v1
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASwriteItemCompressedWavepacket13v1 struct {
	enc *ArithmeticEncoder

	lastItem          [28]byte
	lastDiff32        int32
	symLastOffsetDiff uint32

	mPacketIndex  *ArithmeticModel
	mOffsetDiff   [4]*ArithmeticModel
	icOffsetDiff  *IntegerCompressor
	icPacketSize  *IntegerCompressor
	icReturnPoint *IntegerCompressor
	icXYZ         *IntegerCompressor
}

func NewLASwriteItemCompressedWavepacket13v1(enc *ArithmeticEncoder) *LASwriteItemCompressedWavepacket13v1 {
	w := &LASwriteItemCompressedWavepacket13v1{enc: enc}
	w.mPacketIndex = enc.CreateSymbolModel(256)
	w.mOffsetDiff[0] = enc.CreateSymbolModel(4)
	w.mOffsetDiff[1] = enc.CreateSymbolModel(4)
	w.mOffsetDiff[2] = enc.CreateSymbolModel(4)
	w.mOffsetDiff[3] = enc.CreateSymbolModel(4)
	w.icOffsetDiff = NewIntegerCompressor(enc, 32, 1, 8, 0)
	w.icPacketSize = NewIntegerCompressor(enc, 32, 1, 8, 0)
	w.icReturnPoint = NewIntegerCompressor(enc, 32, 1, 8, 0)
	w.icXYZ = NewIntegerCompressor(enc, 32, 3, 8, 0)
	return w
}

func (w *LASwriteItemCompressedWavepacket13v1) ChunkSizes() error { return nil }
func (w *LASwriteItemCompressedWavepacket13v1) ChunkBytes() error { return nil }

func (w *LASwriteItemCompressedWavepacket13v1) Init(item []byte, _ *uint32) error {
	w.lastDiff32 = 0
	w.symLastOffsetDiff = 0

	w.enc.InitSymbolModel(w.mPacketIndex, nil)
	w.enc.InitSymbolModel(w.mOffsetDiff[0], nil)
	w.enc.InitSymbolModel(w.mOffsetDiff[1], nil)
	w.enc.InitSymbolModel(w.mOffsetDiff[2], nil)
	w.enc.InitSymbolModel(w.mOffsetDiff[3], nil)
	w.icOffsetDiff.InitCompressor()
	w.icPacketSize.InitCompressor()
	w.icReturnPoint.InitCompressor()
	w.icXYZ.InitCompressor()

	copy(w.lastItem[:], item[1:29]) // skip byte 0 (wave packet descriptor index)
	return nil
}

func (w *LASwriteItemCompressedWavepacket13v1) Write(item []byte, _ *uint32) error {
	// Byte 0: packet index.
	w.enc.EncodeSymbol(w.mPacketIndex, uint32(item[0]))

	thisWP := UnpackLASwavepacket13(item[1:29])
	lastWP := UnpackLASwavepacket13(w.lastItem[:])

	// Calculate the difference between the two offsets.
	currDiff64 := int64(thisWP.Offset) - int64(lastWP.Offset)
	currDiff32 := int32(currDiff64)

	// If the current difference can be represented with 32 bits.
	if currDiff64 == int64(currDiff32) {
		if currDiff32 == 0 { // current difference is zero
			w.enc.EncodeSymbol(w.mOffsetDiff[w.symLastOffsetDiff], 0)
			w.symLastOffsetDiff = 0
		} else if currDiff32 == int32(lastWP.PacketSize) {
			w.enc.EncodeSymbol(w.mOffsetDiff[w.symLastOffsetDiff], 1)
			w.symLastOffsetDiff = 1
		} else {
			w.enc.EncodeSymbol(w.mOffsetDiff[w.symLastOffsetDiff], 2)
			w.symLastOffsetDiff = 2
			if err := w.icOffsetDiff.Compress(w.lastDiff32, currDiff32, 0); err != nil {
				return err
			}
			w.lastDiff32 = currDiff32
		}
	} else {
		w.enc.EncodeSymbol(w.mOffsetDiff[w.symLastOffsetDiff], 3)
		w.symLastOffsetDiff = 3
		w.enc.WriteInt64(thisWP.Offset)
	}

	if err := w.icPacketSize.Compress(int32(lastWP.PacketSize), int32(thisWP.PacketSize), 0); err != nil {
		return err
	}
	if err := w.icReturnPoint.Compress(int32(math.Float32bits(lastWP.ReturnPoint)), int32(math.Float32bits(thisWP.ReturnPoint)), 0); err != nil {
		return err
	}
	if err := w.icXYZ.Compress(int32(math.Float32bits(lastWP.X)), int32(math.Float32bits(thisWP.X)), 0); err != nil {
		return err
	}
	if err := w.icXYZ.Compress(int32(math.Float32bits(lastWP.Y)), int32(math.Float32bits(thisWP.Y)), 1); err != nil {
		return err
	}
	if err := w.icXYZ.Compress(int32(math.Float32bits(lastWP.Z)), int32(math.Float32bits(thisWP.Z)), 2); err != nil {
		return err
	}

	copy(w.lastItem[:], item[1:29])
	return nil
}
