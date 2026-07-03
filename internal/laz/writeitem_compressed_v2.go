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

// writeitem_compressed_v2.go — v2 compressed point item writers, ported from
// src/laswriteitemcompressed_v2.cpp. Exact mirror images of the v2 readers
// in readitem_compressed_v2.go: identical model sets, contexts, lazy model
// creation keyed by the last value, and last-item update rules.
package laz

import (
	"encoding/binary"
)

// i32Quantize mirrors C++ I32_QUANTIZE on a float32 value:
// v >= 0 ? (I32)(v+0.5f) : (I32)(v-0.5f). The additions happen in float32
// and the conversion truncates toward zero, exactly like the C++ cast.
func i32Quantize(n float32) int32 {
	if n >= 0 {
		return int32(n + 0.5)
	}
	return int32(n - 0.5)
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// POINT10 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASwriteItemCompressedPoint10v2 struct {
	enc *ArithmeticEncoder

	lastItem       [20]byte
	lastIntensity  [16]uint16
	lastXDiffsMed5 [16]StreamingMedian5
	lastYDiffsMed5 [16]StreamingMedian5
	lastHeight     [8]int32

	mChangedValues  *ArithmeticModel
	icIntensity     *IntegerCompressor
	mScanAngleRank  [2]*ArithmeticModel
	icPointSourceID *IntegerCompressor
	mBitByte        [256]*ArithmeticModel
	mClassification [256]*ArithmeticModel
	mUserData       [256]*ArithmeticModel
	icDX            *IntegerCompressor
	icDY            *IntegerCompressor
	icZ             *IntegerCompressor
}

func NewLASwriteItemCompressedPoint10v2(enc *ArithmeticEncoder) *LASwriteItemCompressedPoint10v2 {
	w := &LASwriteItemCompressedPoint10v2{enc: enc}
	w.mChangedValues = enc.CreateSymbolModel(64)
	w.icIntensity = NewIntegerCompressor(enc, 16, 4, 8, 0)
	w.mScanAngleRank[0] = enc.CreateSymbolModel(256)
	w.mScanAngleRank[1] = enc.CreateSymbolModel(256)
	w.icPointSourceID = NewIntegerCompressor(enc, 16, 1, 8, 0)
	w.icDX = NewIntegerCompressor(enc, 32, 2, 8, 0)  // 32 bits, 2 contexts
	w.icDY = NewIntegerCompressor(enc, 32, 22, 8, 0) // 32 bits, 22 contexts
	w.icZ = NewIntegerCompressor(enc, 32, 20, 8, 0)  // 32 bits, 20 contexts
	return w
}

func (w *LASwriteItemCompressedPoint10v2) ChunkSizes() error { return nil }
func (w *LASwriteItemCompressedPoint10v2) ChunkBytes() error { return nil }

func (w *LASwriteItemCompressedPoint10v2) Init(item []byte, _ *uint32) error {
	for i := range 16 {
		w.lastXDiffsMed5[i].Init()
		w.lastYDiffsMed5[i].Init()
		w.lastIntensity[i] = 0
		w.lastHeight[i/2] = 0
	}
	w.enc.InitSymbolModel(w.mChangedValues, nil)
	w.icIntensity.InitCompressor()
	w.enc.InitSymbolModel(w.mScanAngleRank[0], nil)
	w.enc.InitSymbolModel(w.mScanAngleRank[1], nil)
	w.icPointSourceID.InitCompressor()
	for i := range 256 {
		if w.mBitByte[i] != nil {
			w.enc.InitSymbolModel(w.mBitByte[i], nil)
		}
		if w.mClassification[i] != nil {
			w.enc.InitSymbolModel(w.mClassification[i], nil)
		}
		if w.mUserData[i] != nil {
			w.enc.InitSymbolModel(w.mUserData[i], nil)
		}
	}
	w.icDX.InitCompressor()
	w.icDY.InitCompressor()
	w.icZ.InitCompressor()
	copy(w.lastItem[:], item[:20])
	return nil
}

func (w *LASwriteItemCompressedPoint10v2) Write(item []byte, _ *uint32) error {
	r := uint32(item[14] & 0x07)
	n := uint32((item[14] >> 3) & 0x07)
	m := uint32(NumberReturnMap[n][r])
	l := uint32(NumberReturnLevel[n][r])

	intensity := binary.LittleEndian.Uint16(item[12:14])
	psID := binary.LittleEndian.Uint16(item[18:20])
	lastPsID := binary.LittleEndian.Uint16(w.lastItem[18:20])

	// Compress which other values have changed.
	changedValues := (boolU32(w.lastItem[14] != item[14]) << 5) | // bit_byte
		(boolU32(w.lastIntensity[m] != intensity) << 4) |
		(boolU32(w.lastItem[15] != item[15]) << 3) | // classification
		(boolU32(w.lastItem[16] != item[16]) << 2) | // scan_angle_rank
		(boolU32(w.lastItem[17] != item[17]) << 1) | // user_data
		boolU32(lastPsID != psID)

	w.enc.EncodeSymbol(w.mChangedValues, changedValues)

	// Compress the bit_byte (returns, scan direction, eofl) if it has changed.
	if changedValues&32 != 0 {
		idx := int(w.lastItem[14])
		if w.mBitByte[idx] == nil {
			w.mBitByte[idx] = w.enc.CreateSymbolModel(256)
			w.enc.InitSymbolModel(w.mBitByte[idx], nil)
		}
		w.enc.EncodeSymbol(w.mBitByte[idx], uint32(item[14]))
	}

	// Compress the intensity if it has changed.
	if changedValues&16 != 0 {
		if err := w.icIntensity.Compress(int32(w.lastIntensity[m]), int32(intensity), min(m, 3)); err != nil {
			return err
		}
		w.lastIntensity[m] = intensity
	}

	// Compress the classification if it has changed.
	if changedValues&8 != 0 {
		idx := int(w.lastItem[15])
		if w.mClassification[idx] == nil {
			w.mClassification[idx] = w.enc.CreateSymbolModel(256)
			w.enc.InitSymbolModel(w.mClassification[idx], nil)
		}
		w.enc.EncodeSymbol(w.mClassification[idx], uint32(item[15]))
	}

	// Compress the scan_angle_rank if it has changed.
	if changedValues&4 != 0 {
		scanDir := (item[14] >> 6) & 0x01
		w.enc.EncodeSymbol(w.mScanAngleRank[scanDir], uint32(u8Fold(int(item[16])-int(w.lastItem[16]))))
	}

	// Compress the user_data if it has changed.
	if changedValues&2 != 0 {
		idx := int(w.lastItem[17])
		if w.mUserData[idx] == nil {
			w.mUserData[idx] = w.enc.CreateSymbolModel(256)
			w.enc.InitSymbolModel(w.mUserData[idx], nil)
		}
		w.enc.EncodeSymbol(w.mUserData[idx], uint32(item[17]))
	}

	// Compress the point_source_ID if it has changed.
	if changedValues&1 != 0 {
		if err := w.icPointSourceID.Compress(int32(lastPsID), int32(psID), 0); err != nil {
			return err
		}
	}

	// Compress x coordinate.
	median := w.lastXDiffsMed5[m].Get()
	diff := int32(binary.LittleEndian.Uint32(item[0:4])) - int32(binary.LittleEndian.Uint32(w.lastItem[0:4]))
	if err := w.icDX.Compress(median, diff, boolU32(n == 1)); err != nil {
		return err
	}
	w.lastXDiffsMed5[m].Add(diff)

	// Compress y coordinate.
	kBits := w.icDX.GetK()
	median = w.lastYDiffsMed5[m].Get()
	diff = int32(binary.LittleEndian.Uint32(item[4:8])) - int32(binary.LittleEndian.Uint32(w.lastItem[4:8]))
	yCtx := boolU32(n == 1)
	if kBits < 20 {
		yCtx += u32ZeroBit0(kBits)
	} else {
		yCtx += 20
	}
	if err := w.icDY.Compress(median, diff, yCtx); err != nil {
		return err
	}
	w.lastYDiffsMed5[m].Add(diff)

	// Compress z coordinate.
	kBits = (w.icDX.GetK() + w.icDY.GetK()) / 2
	zCtx := boolU32(n == 1)
	if kBits < 18 {
		zCtx += u32ZeroBit0(kBits)
	} else {
		zCtx += 18
	}
	z := int32(binary.LittleEndian.Uint32(item[8:12]))
	if err := w.icZ.Compress(w.lastHeight[l], z, zCtx); err != nil {
		return err
	}
	w.lastHeight[l] = z

	// Copy the last item.
	copy(w.lastItem[:], item[:20])
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// GPSTIME11 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASwriteItemCompressedGpsTime11v2 struct {
	enc *ArithmeticEncoder

	last, next          uint32
	lastGPSTime         [4]uint64
	lastGPSTimeDiff     [4]int32
	multiExtremeCounter [4]int32

	mGPSTimeMulti *ArithmeticModel
	mGPSTime0diff *ArithmeticModel
	icGPSTime     *IntegerCompressor
}

func NewLASwriteItemCompressedGpsTime11v2(enc *ArithmeticEncoder) *LASwriteItemCompressedGpsTime11v2 {
	w := &LASwriteItemCompressedGpsTime11v2{enc: enc}
	w.mGPSTimeMulti = enc.CreateSymbolModel(gpstimeMultiTotal2)
	w.mGPSTime0diff = enc.CreateSymbolModel(6)
	w.icGPSTime = NewIntegerCompressor(enc, 32, 9, 8, 0) // 32 bits, 9 contexts
	return w
}

func (w *LASwriteItemCompressedGpsTime11v2) ChunkSizes() error { return nil }
func (w *LASwriteItemCompressedGpsTime11v2) ChunkBytes() error { return nil }

func (w *LASwriteItemCompressedGpsTime11v2) Init(item []byte, _ *uint32) error {
	w.last, w.next = 0, 0
	for i := range 4 {
		w.lastGPSTimeDiff[i] = 0
		w.multiExtremeCounter[i] = 0
	}
	w.enc.InitSymbolModel(w.mGPSTimeMulti, nil)
	w.enc.InitSymbolModel(w.mGPSTime0diff, nil)
	w.icGPSTime.InitCompressor()
	w.lastGPSTime[0] = binary.LittleEndian.Uint64(item[:8])
	w.lastGPSTime[1], w.lastGPSTime[2], w.lastGPSTime[3] = 0, 0, 0
	return nil
}

func (w *LASwriteItemCompressedGpsTime11v2) Write(item []byte, context *uint32) error {
	thisGPSTime := int64(binary.LittleEndian.Uint64(item[:8]))

	if w.lastGPSTimeDiff[w.last] == 0 { // if the last integer difference was zero
		if thisGPSTime == int64(w.lastGPSTime[w.last]) {
			w.enc.EncodeSymbol(w.mGPSTime0diff, 0) // the doubles have not changed
		} else {
			// Calculate the difference between the two doubles as an integer.
			currGPSTimeDiff64 := thisGPSTime - int64(w.lastGPSTime[w.last])
			currGPSTimeDiff := int32(currGPSTimeDiff64)
			if currGPSTimeDiff64 == int64(currGPSTimeDiff) {
				w.enc.EncodeSymbol(w.mGPSTime0diff, 1) // difference fits in 32 bits
				if err := w.icGPSTime.Compress(0, currGPSTimeDiff, 0); err != nil {
					return err
				}
				w.lastGPSTimeDiff[w.last] = currGPSTimeDiff
				w.multiExtremeCounter[w.last] = 0
			} else { // the difference is huge
				// Maybe the double belongs to another time sequence.
				for i := uint32(1); i < 4; i++ {
					otherGPSTimeDiff64 := thisGPSTime - int64(w.lastGPSTime[(w.last+i)&3])
					otherGPSTimeDiff := int32(otherGPSTimeDiff64)
					if otherGPSTimeDiff64 == int64(otherGPSTimeDiff) {
						w.enc.EncodeSymbol(w.mGPSTime0diff, i+2) // it belongs to another sequence
						w.last = (w.last + i) & 3
						return w.Write(item, context)
					}
				}
				// No other sequence found. Start new sequence.
				w.enc.EncodeSymbol(w.mGPSTime0diff, 2)
				if err := w.icGPSTime.Compress(int32(w.lastGPSTime[w.last]>>32), int32(uint64(thisGPSTime)>>32), 8); err != nil {
					return err
				}
				w.enc.WriteInt(uint32(uint64(thisGPSTime)))
				w.next = (w.next + 1) & 3
				w.last = w.next
				w.lastGPSTimeDiff[w.last] = 0
				w.multiExtremeCounter[w.last] = 0
			}
			w.lastGPSTime[w.last] = uint64(thisGPSTime)
		}
	} else { // the last integer difference was *not* zero
		if thisGPSTime == int64(w.lastGPSTime[w.last]) {
			// If the doubles have not changed use a special symbol.
			w.enc.EncodeSymbol(w.mGPSTimeMulti, gpstimeMultiUnchanged2)
		} else {
			// Calculate the difference between the two doubles as an integer.
			currGPSTimeDiff64 := thisGPSTime - int64(w.lastGPSTime[w.last])
			currGPSTimeDiff := int32(currGPSTimeDiff64)

			// If the current gpstime difference can be represented with 32 bits.
			if currGPSTimeDiff64 == int64(currGPSTimeDiff) {
				// Compute multiplier between current and last integer difference.
				// This MUST be a float32 division: the quantized result drives
				// the symbol stream (C++ F32 arithmetic).
				multiF := float32(currGPSTimeDiff) / float32(w.lastGPSTimeDiff[w.last])
				multi := i32Quantize(multiF)

				// Compress the residual currGPSTimeDiff in dependence on the multiplier.
				if multi == 1 {
					// The case we expect most often for regularly spaced pulses.
					w.enc.EncodeSymbol(w.mGPSTimeMulti, 1)
					if err := w.icGPSTime.Compress(w.lastGPSTimeDiff[w.last], currGPSTimeDiff, 1); err != nil {
						return err
					}
					w.multiExtremeCounter[w.last] = 0
				} else if multi > 0 {
					if multi < gpstimeMulti2 { // positive multipliers up to MULTI are compressed directly
						w.enc.EncodeSymbol(w.mGPSTimeMulti, uint32(multi))
						var ctx uint32 = 3
						if multi < 10 {
							ctx = 2
						}
						if err := w.icGPSTime.Compress(multi*w.lastGPSTimeDiff[w.last], currGPSTimeDiff, ctx); err != nil {
							return err
						}
					} else {
						w.enc.EncodeSymbol(w.mGPSTimeMulti, gpstimeMulti2)
						if err := w.icGPSTime.Compress(int32(gpstimeMulti2)*w.lastGPSTimeDiff[w.last], currGPSTimeDiff, 4); err != nil {
							return err
						}
						w.multiExtremeCounter[w.last]++
						if w.multiExtremeCounter[w.last] > 3 {
							w.lastGPSTimeDiff[w.last] = currGPSTimeDiff
							w.multiExtremeCounter[w.last] = 0
						}
					}
				} else if multi < 0 {
					if multi > gpstimeMultiMinus2 { // negative multipliers larger than MULTI_MINUS are compressed directly
						w.enc.EncodeSymbol(w.mGPSTimeMulti, uint32(gpstimeMulti2-multi))
						if err := w.icGPSTime.Compress(multi*w.lastGPSTimeDiff[w.last], currGPSTimeDiff, 5); err != nil {
							return err
						}
					} else {
						w.enc.EncodeSymbol(w.mGPSTimeMulti, uint32(gpstimeMulti2-gpstimeMultiMinus2))
						if err := w.icGPSTime.Compress(int32(gpstimeMultiMinus2)*w.lastGPSTimeDiff[w.last], currGPSTimeDiff, 6); err != nil {
							return err
						}
						w.multiExtremeCounter[w.last]++
						if w.multiExtremeCounter[w.last] > 3 {
							w.lastGPSTimeDiff[w.last] = currGPSTimeDiff
							w.multiExtremeCounter[w.last] = 0
						}
					}
				} else {
					w.enc.EncodeSymbol(w.mGPSTimeMulti, 0)
					if err := w.icGPSTime.Compress(0, currGPSTimeDiff, 7); err != nil {
						return err
					}
					w.multiExtremeCounter[w.last]++
					if w.multiExtremeCounter[w.last] > 3 {
						w.lastGPSTimeDiff[w.last] = currGPSTimeDiff
						w.multiExtremeCounter[w.last] = 0
					}
				}
			} else { // the difference is huge
				// Maybe the double belongs to another time sequence.
				for i := uint32(1); i < 4; i++ {
					otherGPSTimeDiff64 := thisGPSTime - int64(w.lastGPSTime[(w.last+i)&3])
					otherGPSTimeDiff := int32(otherGPSTimeDiff64)
					if otherGPSTimeDiff64 == int64(otherGPSTimeDiff) {
						// It belongs to this sequence.
						w.enc.EncodeSymbol(w.mGPSTimeMulti, gpstimeMultiCodeFull2+i)
						w.last = (w.last + i) & 3
						return w.Write(item, context)
					}
				}
				// No other sequence found. Start new sequence.
				w.enc.EncodeSymbol(w.mGPSTimeMulti, gpstimeMultiCodeFull2)
				if err := w.icGPSTime.Compress(int32(w.lastGPSTime[w.last]>>32), int32(uint64(thisGPSTime)>>32), 8); err != nil {
					return err
				}
				w.enc.WriteInt(uint32(uint64(thisGPSTime)))
				w.next = (w.next + 1) & 3
				w.last = w.next
				w.lastGPSTimeDiff[w.last] = 0
				w.multiExtremeCounter[w.last] = 0
			}
			w.lastGPSTime[w.last] = uint64(thisGPSTime)
		}
	}
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// RGB12 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASwriteItemCompressedRGB12v2 struct {
	enc      *ArithmeticEncoder
	lastItem [3]uint16

	mByteUsed *ArithmeticModel
	mRGBDiff0 *ArithmeticModel
	mRGBDiff1 *ArithmeticModel
	mRGBDiff2 *ArithmeticModel
	mRGBDiff3 *ArithmeticModel
	mRGBDiff4 *ArithmeticModel
	mRGBDiff5 *ArithmeticModel
}

func NewLASwriteItemCompressedRGB12v2(enc *ArithmeticEncoder) *LASwriteItemCompressedRGB12v2 {
	w := &LASwriteItemCompressedRGB12v2{enc: enc}
	w.mByteUsed = enc.CreateSymbolModel(128)
	w.mRGBDiff0 = enc.CreateSymbolModel(256)
	w.mRGBDiff1 = enc.CreateSymbolModel(256)
	w.mRGBDiff2 = enc.CreateSymbolModel(256)
	w.mRGBDiff3 = enc.CreateSymbolModel(256)
	w.mRGBDiff4 = enc.CreateSymbolModel(256)
	w.mRGBDiff5 = enc.CreateSymbolModel(256)
	return w
}

func (w *LASwriteItemCompressedRGB12v2) ChunkSizes() error { return nil }
func (w *LASwriteItemCompressedRGB12v2) ChunkBytes() error { return nil }

func (w *LASwriteItemCompressedRGB12v2) Init(item []byte, _ *uint32) error {
	w.enc.InitSymbolModel(w.mByteUsed, nil)
	w.enc.InitSymbolModel(w.mRGBDiff0, nil)
	w.enc.InitSymbolModel(w.mRGBDiff1, nil)
	w.enc.InitSymbolModel(w.mRGBDiff2, nil)
	w.enc.InitSymbolModel(w.mRGBDiff3, nil)
	w.enc.InitSymbolModel(w.mRGBDiff4, nil)
	w.enc.InitSymbolModel(w.mRGBDiff5, nil)
	w.lastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	w.lastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	w.lastItem[2] = binary.LittleEndian.Uint16(item[4:6])
	return nil
}

func (w *LASwriteItemCompressedRGB12v2) Write(item []byte, _ *uint32) error {
	var in [3]uint16
	in[0] = binary.LittleEndian.Uint16(item[0:2])
	in[1] = binary.LittleEndian.Uint16(item[2:4])
	in[2] = binary.LittleEndian.Uint16(item[4:6])

	diffL := int32(0)
	diffH := int32(0)
	var corr int32

	sym := boolU32((w.lastItem[0] & 0x00FF) != (in[0] & 0x00FF))
	sym |= boolU32((w.lastItem[0]&0xFF00) != (in[0]&0xFF00)) << 1
	sym |= boolU32((w.lastItem[1]&0x00FF) != (in[1]&0x00FF)) << 2
	sym |= boolU32((w.lastItem[1]&0xFF00) != (in[1]&0xFF00)) << 3
	sym |= boolU32((w.lastItem[2]&0x00FF) != (in[2]&0x00FF)) << 4
	sym |= boolU32((w.lastItem[2]&0xFF00) != (in[2]&0xFF00)) << 5
	sym |= boolU32((in[0]&0x00FF) != (in[1]&0x00FF) ||
		(in[0]&0x00FF) != (in[2]&0x00FF) ||
		(in[0]&0xFF00) != (in[1]&0xFF00) ||
		(in[0]&0xFF00) != (in[2]&0xFF00)) << 6

	w.enc.EncodeSymbol(w.mByteUsed, sym)

	if sym&(1<<0) != 0 {
		diffL = int32(in[0]&255) - int32(w.lastItem[0]&255)
		w.enc.EncodeSymbol(w.mRGBDiff0, uint32(u8Fold(int(diffL))))
	}
	if sym&(1<<1) != 0 {
		diffH = int32(in[0]>>8) - int32(w.lastItem[0]>>8)
		w.enc.EncodeSymbol(w.mRGBDiff1, uint32(u8Fold(int(diffH))))
	}
	if sym&(1<<6) != 0 {
		if sym&(1<<2) != 0 {
			corr = int32(in[1]&255) - int32(u8Clamp(diffL+int32(w.lastItem[1]&255)))
			w.enc.EncodeSymbol(w.mRGBDiff2, uint32(u8Fold(int(corr))))
		}
		if sym&(1<<4) != 0 {
			diffL = (diffL + int32(in[1]&255) - int32(w.lastItem[1]&255)) / 2
			corr = int32(in[2]&255) - int32(u8Clamp(diffL+int32(w.lastItem[2]&255)))
			w.enc.EncodeSymbol(w.mRGBDiff4, uint32(u8Fold(int(corr))))
		}
		if sym&(1<<3) != 0 {
			corr = int32(in[1]>>8) - int32(u8Clamp(diffH+int32(w.lastItem[1]>>8)))
			w.enc.EncodeSymbol(w.mRGBDiff3, uint32(u8Fold(int(corr))))
		}
		if sym&(1<<5) != 0 {
			diffH = (diffH + int32(in[1]>>8) - int32(w.lastItem[1]>>8)) / 2
			corr = int32(in[2]>>8) - int32(u8Clamp(diffH+int32(w.lastItem[2]>>8)))
			w.enc.EncodeSymbol(w.mRGBDiff5, uint32(u8Fold(int(corr))))
		}
	}

	w.lastItem = in
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// BYTE v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASwriteItemCompressedBytev2 struct {
	enc      *ArithmeticEncoder
	number   uint32
	lastItem []byte
	mByte    []*ArithmeticModel
}

func NewLASwriteItemCompressedBytev2(enc *ArithmeticEncoder, number uint32) *LASwriteItemCompressedBytev2 {
	w := &LASwriteItemCompressedBytev2{enc: enc, number: number}
	w.lastItem = make([]byte, number)
	w.mByte = make([]*ArithmeticModel, number)
	for i := range number {
		w.mByte[i] = enc.CreateSymbolModel(256)
	}
	return w
}

func (w *LASwriteItemCompressedBytev2) ChunkSizes() error { return nil }
func (w *LASwriteItemCompressedBytev2) ChunkBytes() error { return nil }

func (w *LASwriteItemCompressedBytev2) Init(item []byte, _ *uint32) error {
	for i := uint32(0); i < w.number; i++ {
		w.enc.InitSymbolModel(w.mByte[i], nil)
	}
	copy(w.lastItem, item[:w.number])
	return nil
}

func (w *LASwriteItemCompressedBytev2) Write(item []byte, _ *uint32) error {
	for i := uint32(0); i < w.number; i++ {
		diff := int32(item[i]) - int32(w.lastItem[i])
		w.enc.EncodeSymbol(w.mByte[i], uint32(u8Fold(int(diff))))
	}
	copy(w.lastItem, item[:w.number])
	return nil
}
