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
// readitem_compressed_v1.go — v1 compressed point item readers, ported from
// src/lasreaditemcompressed_v1.hpp/cpp. Decompression path only.
package laz

import (
	"encoding/binary"
	"math"
)

const lasZipGpsTimeMultiMax = 512

// ---------------------------------------------------------------------------
// POINT10 v1 compressed reader
// ---------------------------------------------------------------------------

type LASreadItemCompressedPoint10v1 struct {
	dec *ArithmeticDecoder

	lastItem   [20]byte
	lastXDiffs [3]int32
	lastYDiffs [3]int32
	lastIncr   int32

	icDX            *IntegerCompressor
	icDY            *IntegerCompressor
	icZ             *IntegerCompressor
	icIntensity     *IntegerCompressor
	icScanAngleRank *IntegerCompressor
	icPointSourceID *IntegerCompressor
	mChangedValues  *ArithmeticModel
	mBitByte        [256]*ArithmeticModel
	mClassification [256]*ArithmeticModel
	mUserData       [256]*ArithmeticModel
}

func NewLASreadItemCompressedPoint10v1(dec *ArithmeticDecoder) *LASreadItemCompressedPoint10v1 {
	r := &LASreadItemCompressedPoint10v1{dec: dec}
	r.icDX = NewIntegerDecompressor(dec, 32, 1, 8, 0)
	r.icDY = NewIntegerDecompressor(dec, 32, 20, 8, 0)
	r.icZ = NewIntegerDecompressor(dec, 32, 20, 8, 0)
	r.icIntensity = NewIntegerDecompressor(dec, 16, 1, 8, 0)
	r.icScanAngleRank = NewIntegerDecompressor(dec, 8, 2, 8, 0)
	r.icPointSourceID = NewIntegerDecompressor(dec, 16, 1, 8, 0)
	r.mChangedValues = dec.CreateSymbolModel(64)
	return r
}

func (r *LASreadItemCompressedPoint10v1) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedPoint10v1) Init(item []byte, _ *uint32) error {
	r.lastXDiffs[0], r.lastXDiffs[1], r.lastXDiffs[2] = 0, 0, 0
	r.lastYDiffs[0], r.lastYDiffs[1], r.lastYDiffs[2] = 0, 0, 0
	r.lastIncr = 0

	r.icDX.InitDecompressor()
	r.icDY.InitDecompressor()
	r.icZ.InitDecompressor()
	r.icIntensity.InitDecompressor()
	r.icScanAngleRank.InitDecompressor()
	r.icPointSourceID.InitDecompressor()
	r.dec.InitSymbolModel(r.mChangedValues, nil)
	for i := range 256 {
		if r.mBitByte[i] != nil {
			r.dec.InitSymbolModel(r.mBitByte[i], nil)
		}
		if r.mClassification[i] != nil {
			r.dec.InitSymbolModel(r.mClassification[i], nil)
		}
		if r.mUserData[i] != nil {
			r.dec.InitSymbolModel(r.mUserData[i], nil)
		}
	}
	copy(r.lastItem[:], item[:20])
	return nil
}

func median3(a, b, c int32) int32 {
	if a < b {
		if b < c {
			return b
		} else if a < c {
			return c
		}
		return a
	}
	if a < c {
		return a
	} else if b < c {
		return c
	}
	return b
}

func (r *LASreadItemCompressedPoint10v1) Read(item []byte, _ *uint32) error {
	medX := median3(r.lastXDiffs[0], r.lastXDiffs[1], r.lastXDiffs[2])
	medY := median3(r.lastYDiffs[0], r.lastYDiffs[1], r.lastYDiffs[2])

	// Decompress X, Y, Z
	xDiff, err := r.icDX.Decompress(medX, 0)
	if err != nil {
		return err
	}
	curX := int32(binary.LittleEndian.Uint32(r.lastItem[0:4]))
	curX += xDiff
	binary.LittleEndian.PutUint32(r.lastItem[0:4], uint32(curX))
	kBits := r.icDX.GetK()

	yDiff, err := r.icDY.Decompress(medY, minU32(kBits, 19))
	if err != nil {
		return err
	}
	curY := int32(binary.LittleEndian.Uint32(r.lastItem[4:8]))
	curY += yDiff
	binary.LittleEndian.PutUint32(r.lastItem[4:8], uint32(curY))
	kBits = (kBits + r.icDY.GetK()) / 2

	curZ := int32(binary.LittleEndian.Uint32(r.lastItem[8:12]))
	zVal, err := r.icZ.Decompress(curZ, minU32(kBits, 19))
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(r.lastItem[8:12], uint32(zVal))

	changed, err := r.dec.DecodeSymbol(r.mChangedValues)
	if err != nil {
		return err
	}
	if changed != 0 {
		if changed&32 != 0 {
			curIntensity := int32(binary.LittleEndian.Uint16(r.lastItem[12:14]))
			val, err := r.icIntensity.Decompress(curIntensity, 0)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(r.lastItem[12:14], uint16(val))
		}
		if changed&16 != 0 {
			idx := int(r.lastItem[14])
			if r.mBitByte[idx] == nil {
				r.mBitByte[idx] = r.dec.CreateSymbolModel(256)
				r.dec.InitSymbolModel(r.mBitByte[idx], nil)
			}
			val, err := r.dec.DecodeSymbol(r.mBitByte[idx])
			if err != nil {
				return err
			}
			r.lastItem[14] = byte(val)
		}
		if changed&8 != 0 {
			idx := int(r.lastItem[15])
			if r.mClassification[idx] == nil {
				r.mClassification[idx] = r.dec.CreateSymbolModel(256)
				r.dec.InitSymbolModel(r.mClassification[idx], nil)
			}
			val, err := r.dec.DecodeSymbol(r.mClassification[idx])
			if err != nil {
				return err
			}
			r.lastItem[15] = byte(val)
		}
		if changed&4 != 0 {
			ctx := uint32(0)
			if kBits < 3 {
				ctx = 1
			}
			val, err := r.icScanAngleRank.Decompress(int32(int8(r.lastItem[16])), ctx)
			if err != nil {
				return err
			}
			r.lastItem[16] = byte(val)
		}
		if changed&2 != 0 {
			idx := int(r.lastItem[17])
			if r.mUserData[idx] == nil {
				r.mUserData[idx] = r.dec.CreateSymbolModel(256)
				r.dec.InitSymbolModel(r.mUserData[idx], nil)
			}
			val, err := r.dec.DecodeSymbol(r.mUserData[idx])
			if err != nil {
				return err
			}
			r.lastItem[17] = byte(val)
		}
		if changed&1 != 0 {
			curPSID := int32(binary.LittleEndian.Uint16(r.lastItem[18:20]))
			val, err := r.icPointSourceID.Decompress(curPSID, 0)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(r.lastItem[18:20], uint16(val))
		}
	}

	r.lastXDiffs[r.lastIncr] = xDiff
	r.lastYDiffs[r.lastIncr] = yDiff
	r.lastIncr++
	if r.lastIncr > 2 {
		r.lastIncr = 0
	}
	copy(item[:20], r.lastItem[:])
	return nil
}

// ---------------------------------------------------------------------------
// GPSTIME11 v1 compressed reader
// ---------------------------------------------------------------------------

type LASreadItemCompressedGpsTime11v1 struct {
	dec *ArithmeticDecoder

	lastGPSTime         uint64 // stored as union U64
	lastGPSTimeDiff     int32
	multiExtremeCounter int32

	mGPSTimeMulti *ArithmeticModel
	mGPSTime0diff *ArithmeticModel
	icGPSTime     *IntegerCompressor
}

func NewLASreadItemCompressedGpsTime11v1(dec *ArithmeticDecoder) *LASreadItemCompressedGpsTime11v1 {
	r := &LASreadItemCompressedGpsTime11v1{dec: dec}
	r.mGPSTimeMulti = dec.CreateSymbolModel(lasZipGpsTimeMultiMax)
	r.mGPSTime0diff = dec.CreateSymbolModel(3)
	r.icGPSTime = NewIntegerDecompressor(dec, 32, 6, 8, 0)
	return r
}

func (r *LASreadItemCompressedGpsTime11v1) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedGpsTime11v1) Init(item []byte, _ *uint32) error {
	r.lastGPSTimeDiff = 0
	r.multiExtremeCounter = 0
	r.dec.InitSymbolModel(r.mGPSTimeMulti, nil)
	r.dec.InitSymbolModel(r.mGPSTime0diff, nil)
	r.icGPSTime.InitDecompressor()
	r.lastGPSTime = binary.LittleEndian.Uint64(item[:8])
	return nil
}

func (r *LASreadItemCompressedGpsTime11v1) Read(item []byte, _ *uint32) error {
	var multi uint32
	var err error

	if r.lastGPSTimeDiff == 0 {
		multi, err = r.dec.DecodeSymbol(r.mGPSTime0diff)
		if err != nil {
			return err
		}
		if multi == 1 {
			r.lastGPSTimeDiff, err = r.icGPSTime.Decompress(0, 0)
			if err != nil {
				return err
			}
			r.lastGPSTime += uint64(int64(r.lastGPSTimeDiff))
		} else if multi == 2 {
			r.lastGPSTime, err = r.dec.ReadInt64()
			if err != nil {
				return err
			}
		}
	} else {
		multi, err = r.dec.DecodeSymbol(r.mGPSTimeMulti)
		if err != nil {
			return err
		}
		if multi < lasZipGpsTimeMultiMax-2 {
			var diff int32
			if multi == 1 {
				diff, err = r.icGPSTime.Decompress(r.lastGPSTimeDiff, 1)
				if err != nil {
					return err
				}
				r.lastGPSTimeDiff = diff
				r.multiExtremeCounter = 0
			} else if multi == 0 {
				diff, err = r.icGPSTime.Decompress(r.lastGPSTimeDiff/4, 2)
				if err != nil {
					return err
				}
				r.multiExtremeCounter++
				if r.multiExtremeCounter > 3 {
					r.lastGPSTimeDiff = diff
					r.multiExtremeCounter = 0
				}
			} else if multi < 10 {
				diff, err = r.icGPSTime.Decompress(int32(multi)*r.lastGPSTimeDiff, 3)
				if err != nil {
					return err
				}
			} else if multi < 50 {
				diff, err = r.icGPSTime.Decompress(int32(multi)*r.lastGPSTimeDiff, 4)
				if err != nil {
					return err
				}
			} else {
				diff, err = r.icGPSTime.Decompress(int32(multi)*r.lastGPSTimeDiff, 5)
				if err != nil {
					return err
				}
				if multi == lasZipGpsTimeMultiMax-3 {
					r.multiExtremeCounter++
					if r.multiExtremeCounter > 3 {
						r.lastGPSTimeDiff = diff
						r.multiExtremeCounter = 0
					}
				}
			}
			r.lastGPSTime += uint64(int64(diff))
		} else if multi < lasZipGpsTimeMultiMax-1 {
			r.lastGPSTime, err = r.dec.ReadInt64()
			if err != nil {
				return err
			}
		}
	}

	binary.LittleEndian.PutUint64(item[:8], r.lastGPSTime)
	return nil
}

// ---------------------------------------------------------------------------
// RGB12 v1 compressed reader
// ---------------------------------------------------------------------------

type LASreadItemCompressedRGB12v1 struct {
	dec      *ArithmeticDecoder
	lastItem [6]byte

	mByteUsed *ArithmeticModel
	icRGB     *IntegerCompressor
}

func NewLASreadItemCompressedRGB12v1(dec *ArithmeticDecoder) *LASreadItemCompressedRGB12v1 {
	r := &LASreadItemCompressedRGB12v1{dec: dec}
	r.mByteUsed = dec.CreateSymbolModel(64)
	r.icRGB = NewIntegerDecompressor(dec, 8, 6, 8, 0)
	return r
}

func (r *LASreadItemCompressedRGB12v1) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedRGB12v1) Init(item []byte, _ *uint32) error {
	r.dec.InitSymbolModel(r.mByteUsed, nil)
	r.icRGB.InitDecompressor()
	copy(r.lastItem[:], item[:6])
	return nil
}

func (r *LASreadItemCompressedRGB12v1) Read(item []byte, _ *uint32) error {
	sym, err := r.dec.DecodeSymbol(r.mByteUsed)
	if err != nil {
		return err
	}
	if sym&(1<<0) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[0]), 0)
		if err != nil {
			return err
		}
		item[0] = byte(val)
	} else {
		item[0] = r.lastItem[0]
	}
	if sym&(1<<1) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[1]), 1)
		if err != nil {
			return err
		}
		item[1] = byte(val)
	} else {
		item[1] = r.lastItem[1]
	}
	if sym&(1<<2) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[2]), 2)
		if err != nil {
			return err
		}
		item[2] = byte(val)
	} else {
		item[2] = r.lastItem[2]
	}
	if sym&(1<<3) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[3]), 3)
		if err != nil {
			return err
		}
		item[3] = byte(val)
	} else {
		item[3] = r.lastItem[3]
	}
	if sym&(1<<4) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[4]), 4)
		if err != nil {
			return err
		}
		item[4] = byte(val)
	} else {
		item[4] = r.lastItem[4]
	}
	if sym&(1<<5) != 0 {
		val, err := r.icRGB.Decompress(int32(r.lastItem[5]), 5)
		if err != nil {
			return err
		}
		item[5] = byte(val)
	} else {
		item[5] = r.lastItem[5]
	}
	copy(r.lastItem[:], item[:6])
	return nil
}

// ---------------------------------------------------------------------------
// WAVEPACKET13 v1 compressed reader
// ---------------------------------------------------------------------------

type LASreadItemCompressedWavepacket13v1 struct {
	dec *ArithmeticDecoder

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

func NewLASreadItemCompressedWavepacket13v1(dec *ArithmeticDecoder) *LASreadItemCompressedWavepacket13v1 {
	r := &LASreadItemCompressedWavepacket13v1{dec: dec}
	r.mPacketIndex = dec.CreateSymbolModel(256)
	r.mOffsetDiff[0] = dec.CreateSymbolModel(4)
	r.mOffsetDiff[1] = dec.CreateSymbolModel(4)
	r.mOffsetDiff[2] = dec.CreateSymbolModel(4)
	r.mOffsetDiff[3] = dec.CreateSymbolModel(4)
	r.icOffsetDiff = NewIntegerDecompressor(dec, 32, 1, 8, 0)
	r.icPacketSize = NewIntegerDecompressor(dec, 32, 1, 8, 0)
	r.icReturnPoint = NewIntegerDecompressor(dec, 32, 1, 8, 0)
	r.icXYZ = NewIntegerDecompressor(dec, 32, 3, 8, 0)
	return r
}

func (r *LASreadItemCompressedWavepacket13v1) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedWavepacket13v1) Init(item []byte, _ *uint32) error {
	r.lastDiff32 = 0
	r.symLastOffsetDiff = 0

	r.dec.InitSymbolModel(r.mPacketIndex, nil)
	r.dec.InitSymbolModel(r.mOffsetDiff[0], nil)
	r.dec.InitSymbolModel(r.mOffsetDiff[1], nil)
	r.dec.InitSymbolModel(r.mOffsetDiff[2], nil)
	r.dec.InitSymbolModel(r.mOffsetDiff[3], nil)
	r.icOffsetDiff.InitDecompressor()
	r.icPacketSize.InitDecompressor()
	r.icReturnPoint.InitDecompressor()
	r.icXYZ.InitDecompressor()

	copy(r.lastItem[:], item[1:29]) // skip byte 0 (wave packet descriptor index)
	return nil
}

func (r *LASreadItemCompressedWavepacket13v1) Read(item []byte, _ *uint32) error {
	// Byte 0: packet index
	val, err := r.dec.DecodeSymbol(r.mPacketIndex)
	if err != nil {
		return err
	}
	item[0] = byte(val)

	// Unpack last wave packet
	lastWP := UnpackLASwavepacket13(r.lastItem[:])

	// Decode offset
	r.symLastOffsetDiff, err = r.dec.DecodeSymbol(r.mOffsetDiff[r.symLastOffsetDiff])
	if err != nil {
		return err
	}

	var wp LASwavepacket13
	switch r.symLastOffsetDiff {
	case 0:
		wp.Offset = lastWP.Offset
	case 1:
		wp.Offset = lastWP.Offset + uint64(lastWP.PacketSize)
	case 2:
		r.lastDiff32, err = r.icOffsetDiff.Decompress(r.lastDiff32, 0)
		if err != nil {
			return err
		}
		wp.Offset = lastWP.Offset + uint64(r.lastDiff32)
	default:
		wp.Offset, err = r.dec.ReadInt64()
		if err != nil {
			return err
		}
	}

	// Decode remaining fields
	ps, err := r.icPacketSize.Decompress(int32(lastWP.PacketSize), 0)
	if err != nil {
		return err
	}
	wp.PacketSize = uint32(ps)

	rp, err := r.icReturnPoint.Decompress(int32(math.Float32bits(lastWP.ReturnPoint)), 0)
	if err != nil {
		return err
	}
	wp.ReturnPoint = math.Float32frombits(uint32(rp))

	rx, err := r.icXYZ.Decompress(int32(math.Float32bits(lastWP.X)), 0)
	if err != nil {
		return err
	}
	wp.X = math.Float32frombits(uint32(rx))

	ry, err := r.icXYZ.Decompress(int32(math.Float32bits(lastWP.Y)), 1)
	if err != nil {
		return err
	}
	wp.Y = math.Float32frombits(uint32(ry))

	rz, err := r.icXYZ.Decompress(int32(math.Float32bits(lastWP.Z)), 2)
	if err != nil {
		return err
	}
	wp.Z = math.Float32frombits(uint32(rz))

	// Pack into output (bytes 1-28), byte 0 already set
	packed := PackLASwavepacket13(&wp)
	copy(item[1:29], packed[:28])
	copy(r.lastItem[:], item[1:29])
	return nil
}

// ---------------------------------------------------------------------------
// BYTE v1 compressed reader
// ---------------------------------------------------------------------------

type LASreadItemCompressedBytev1 struct {
	dec      *ArithmeticDecoder
	number   uint32
	lastItem []byte
	icByte   *IntegerCompressor
}

func NewLASreadItemCompressedBytev1(dec *ArithmeticDecoder, number uint32) *LASreadItemCompressedBytev1 {
	r := &LASreadItemCompressedBytev1{dec: dec, number: number}
	r.lastItem = make([]byte, number)
	r.icByte = NewIntegerDecompressor(dec, 8, number, 8, 0)
	return r
}

func (r *LASreadItemCompressedBytev1) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedBytev1) Init(item []byte, _ *uint32) error {
	r.icByte.InitDecompressor()
	copy(r.lastItem, item[:r.number])
	return nil
}

func (r *LASreadItemCompressedBytev1) Read(item []byte, _ *uint32) error {
	for i := uint32(0); i < r.number; i++ {
		val, err := r.icByte.Decompress(int32(r.lastItem[i]), i)
		if err != nil {
			return err
		}
		item[i] = byte(val)
	}
	copy(r.lastItem, item[:r.number])
	return nil
}

func minU32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}
