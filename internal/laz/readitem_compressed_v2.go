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

package laz

import "encoding/binary"

// GPS time constants shared across v2, v3, v4.
// v2 uses CODE_FULL=512, TOTAL=516 (matches C++ lasreaditemcompressed_v2.cpp).
// v3/v4 use CODE_FULL=511, TOTAL=515 (matches C++ lasreaditemcompressed_v3/v4.cpp).
const (
	gpstimeMulti2          = 500
	gpstimeMultiMinus2     = -10
	gpstimeMultiUnchanged2 = gpstimeMulti2 - gpstimeMultiMinus2 + 1 // 511 (v2 UNCHANGED)
	gpstimeMultiCodeFull2  = gpstimeMulti2 - gpstimeMultiMinus2 + 2 // 512 (v2 CODE_FULL)
	gpstimeMultiTotal2     = gpstimeMulti2 - gpstimeMultiMinus2 + 6 // 516 (v2 total symbols)

	// v3 and v4 use different sentinels (one less than v2):
	gpstimeMultiCodeFullV3 = gpstimeMulti2 - gpstimeMultiMinus2 + 1 // 511 (v3/v4 CODE_FULL)
	gpstimeMultiTotalV3    = gpstimeMulti2 - gpstimeMultiMinus2 + 5 // 515 (v3/v4 total symbols)
)

// u8Clamp mirrors C++ U8_CLAMP(n).
func u8Clamp(n int32) uint8 {
	if n <= 0 {
		return 0
	}
	if n >= 255 {
		return 255
	}
	return uint8(n)
}

// u32ZeroBit0 masks off bit 0 (C++ U32_ZERO_BIT_0).
func u32ZeroBit0(n uint32) uint32 { return n & 0xFFFFFFFE }

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// POINT10 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASreadItemCompressedPoint10v2 struct {
	dec *ArithmeticDecoder

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

func NewLASreadItemCompressedPoint10v2(dec *ArithmeticDecoder) *LASreadItemCompressedPoint10v2 {
	r := &LASreadItemCompressedPoint10v2{dec: dec}
	r.mChangedValues = dec.CreateSymbolModel(64)
	r.icIntensity = NewIntegerDecompressor(dec, 16, 4, 8, 0)
	r.mScanAngleRank[0] = dec.CreateSymbolModel(256)
	r.mScanAngleRank[1] = dec.CreateSymbolModel(256)
	r.icPointSourceID = NewIntegerDecompressor(dec, 16, 1, 8, 0)
	r.icDX = NewIntegerDecompressor(dec, 32, 2, 8, 0)
	r.icDY = NewIntegerDecompressor(dec, 32, 22, 8, 0)
	r.icZ = NewIntegerDecompressor(dec, 32, 20, 8, 0)
	return r
}

func (r *LASreadItemCompressedPoint10v2) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedPoint10v2) Init(item []byte, _ *uint32) error {
	for i := range 16 {
		r.lastXDiffsMed5[i].Init()
		r.lastYDiffsMed5[i].Init()
		r.lastIntensity[i] = 0
	}
	for i := range 8 {
		r.lastHeight[i] = 0
	}
	r.dec.InitSymbolModel(r.mChangedValues, nil)
	r.icIntensity.InitDecompressor()
	r.dec.InitSymbolModel(r.mScanAngleRank[0], nil)
	r.dec.InitSymbolModel(r.mScanAngleRank[1], nil)
	r.icPointSourceID.InitDecompressor()
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
	r.icDX.InitDecompressor()
	r.icDY.InitDecompressor()
	r.icZ.InitDecompressor()
	copy(r.lastItem[:], item[:20])
	r.lastItem[12] = 0
	r.lastItem[13] = 0
	return nil
}

func (r *LASreadItemCompressedPoint10v2) Read(item []byte, _ *uint32) error {
	var rn, nr, m, l uint32
	var kBits uint32

	changed, err := r.dec.DecodeSymbol(r.mChangedValues)
	if err != nil {
		return err
	}

	if changed != 0 {
		if changed&32 != 0 {
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

		// Extract return numbers from updated lastItem
		b14 := r.lastItem[14]
		rn = uint32(b14 & 0x07)
		nr = uint32((b14 >> 3) & 0x07)
		m = uint32(NumberReturnMap[nr][rn])
		l = uint32(NumberReturnLevel[nr][rn])

		if changed&16 != 0 {
			ctx := min(m, 3)
			val, err := r.icIntensity.Decompress(int32(r.lastIntensity[m]), ctx)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(r.lastItem[12:14], uint16(val))
			r.lastIntensity[m] = uint16(val)
		} else {
			binary.LittleEndian.PutUint16(r.lastItem[12:14], r.lastIntensity[m])
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
			scanDir := (b14 >> 6) & 1
			sym, err := r.dec.DecodeSymbol(r.mScanAngleRank[scanDir])
			if err != nil {
				return err
			}
			r.lastItem[16] = u8Fold(int(int32(sym) + int32(int8(r.lastItem[16]))))
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
	} else {
		b14 := r.lastItem[14]
		rn = uint32(b14 & 0x07)
		nr = uint32((b14 >> 3) & 0x07)
		m = uint32(NumberReturnMap[nr][rn])
		l = uint32(NumberReturnLevel[nr][rn])
	}

	// Decompress X
	medX := r.lastXDiffsMed5[m].Get()
	xDiff, err := r.icDX.Decompress(medX, func() uint32 {
		if nr == 1 {
			return 1
		} else {
			return 0
		}
	}())
	if err != nil {
		return err
	}
	curX := int32(binary.LittleEndian.Uint32(r.lastItem[0:4]))
	curX += xDiff
	binary.LittleEndian.PutUint32(r.lastItem[0:4], uint32(curX))
	r.lastXDiffsMed5[m].Add(xDiff)

	// Decompress Y
	medY := r.lastYDiffsMed5[m].Get()
	kBits = r.icDX.GetK()
	yCtx := uint32(0)
	if nr == 1 {
		yCtx++
	}
	if kBits < 20 {
		yCtx += u32ZeroBit0(kBits)
	} else {
		yCtx += 20
	}
	yDiff, err := r.icDY.Decompress(medY, yCtx)
	if err != nil {
		return err
	}
	curY := int32(binary.LittleEndian.Uint32(r.lastItem[4:8]))
	curY += yDiff
	binary.LittleEndian.PutUint32(r.lastItem[4:8], uint32(curY))
	r.lastYDiffsMed5[m].Add(yDiff)

	// Decompress Z
	kBits = (r.icDX.GetK() + r.icDY.GetK()) / 2
	zCtx := uint32(0)
	if nr == 1 {
		zCtx = 1
	}
	if kBits < 18 {
		zCtx += u32ZeroBit0(kBits)
	} else {
		zCtx += 18
	}
	zVal, err := r.icZ.Decompress(r.lastHeight[l], zCtx)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(r.lastItem[8:12], uint32(zVal))
	r.lastHeight[l] = int32(zVal)

	copy(item[:20], r.lastItem[:])
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// GPSTIME11 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASreadItemCompressedGpsTime11v2 struct {
	dec *ArithmeticDecoder

	last, next          uint32
	lastGPSTime         [4]uint64
	lastGPSTimeDiff     [4]int32
	multiExtremeCounter [4]int32

	mGPSTimeMulti *ArithmeticModel
	mGPSTime0diff *ArithmeticModel
	icGPSTime     *IntegerCompressor
}

func NewLASreadItemCompressedGpsTime11v2(dec *ArithmeticDecoder) *LASreadItemCompressedGpsTime11v2 {
	r := &LASreadItemCompressedGpsTime11v2{dec: dec}
	r.mGPSTimeMulti = dec.CreateSymbolModel(gpstimeMultiTotal2)
	r.mGPSTime0diff = dec.CreateSymbolModel(6)
	r.icGPSTime = NewIntegerDecompressor(dec, 32, 9, 8, 0)
	return r
}

func (r *LASreadItemCompressedGpsTime11v2) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedGpsTime11v2) Init(item []byte, _ *uint32) error {
	r.last, r.next = 0, 0
	for i := range 4 {
		r.lastGPSTimeDiff[i] = 0
		r.multiExtremeCounter[i] = 0
	}
	r.dec.InitSymbolModel(r.mGPSTimeMulti, nil)
	r.dec.InitSymbolModel(r.mGPSTime0diff, nil)
	r.icGPSTime.InitDecompressor()
	r.lastGPSTime[0] = binary.LittleEndian.Uint64(item[:8])
	r.lastGPSTime[1], r.lastGPSTime[2], r.lastGPSTime[3] = 0, 0, 0
	return nil
}

func (r *LASreadItemCompressedGpsTime11v2) Read(item []byte, context *uint32) error {
	if r.lastGPSTimeDiff[r.last] == 0 {
		multi, err := r.dec.DecodeSymbol(r.mGPSTime0diff)
		if err != nil {
			return err
		}
		if multi == 1 {
			r.lastGPSTimeDiff[r.last], err = r.icGPSTime.Decompress(0, 0)
			if err != nil {
				return err
			}
			r.lastGPSTime[r.last] += uint64(int64(r.lastGPSTimeDiff[r.last]))
			r.multiExtremeCounter[r.last] = 0
		} else if multi == 2 {
			r.next = (r.next + 1) & 3
			hi, err := r.icGPSTime.Decompress(int32(r.lastGPSTime[r.last]>>32), 8)
			if err != nil {
				return err
			}
			r.lastGPSTime[r.next] = uint64(hi) << 32
			lo, err := r.dec.ReadInt()
			if err != nil {
				return err
			}
			r.lastGPSTime[r.next] |= uint64(lo)
			r.last = r.next
			r.lastGPSTimeDiff[r.last] = 0
			r.multiExtremeCounter[r.last] = 0
		} else if multi > 2 {
			r.last = (r.last + multi - 2) & 3
			return r.Read(item, context) // recursive tail-call to another slot
		}
	} else {
		multi, err := r.dec.DecodeSymbol(r.mGPSTimeMulti)
		if err != nil {
			return err
		}
		if multi == 1 {
			diff, err := r.icGPSTime.Decompress(r.lastGPSTimeDiff[r.last], 1)
			if err != nil {
				return err
			}
			r.lastGPSTime[r.last] += uint64(int64(diff))
			r.multiExtremeCounter[r.last] = 0
		} else if multi < gpstimeMultiUnchanged2 {
			var diff int32
			if multi == 0 {
				diff, err = r.icGPSTime.Decompress(0, 7)
				if err != nil {
					return err
				}
				r.multiExtremeCounter[r.last]++
				if r.multiExtremeCounter[r.last] > 3 {
					r.lastGPSTimeDiff[r.last] = diff
					r.multiExtremeCounter[r.last] = 0
				}
			} else if multi < uint32(gpstimeMulti2) {
				if multi < 10 {
					diff, err = r.icGPSTime.Decompress(int32(multi)*r.lastGPSTimeDiff[r.last], 2)
				} else {
					diff, err = r.icGPSTime.Decompress(int32(multi)*r.lastGPSTimeDiff[r.last], 3)
				}
				if err != nil {
					return err
				}
			} else if multi == uint32(gpstimeMulti2) {
				diff, err = r.icGPSTime.Decompress(int32(gpstimeMulti2)*r.lastGPSTimeDiff[r.last], 4)
				if err != nil {
					return err
				}
				r.multiExtremeCounter[r.last]++
				if r.multiExtremeCounter[r.last] > 3 {
					r.lastGPSTimeDiff[r.last] = diff
					r.multiExtremeCounter[r.last] = 0
				}
			} else {
				multi2 := int32(gpstimeMulti2) - int32(multi)
				if multi2 > int32(gpstimeMultiMinus2) {
					diff, err = r.icGPSTime.Decompress(multi2*r.lastGPSTimeDiff[r.last], 5)
				} else {
					diff, err = r.icGPSTime.Decompress(int32(gpstimeMultiMinus2)*r.lastGPSTimeDiff[r.last], 6)
					if err != nil {
						return err
					}
					r.multiExtremeCounter[r.last]++
					if r.multiExtremeCounter[r.last] > 3 {
						r.lastGPSTimeDiff[r.last] = diff
						r.multiExtremeCounter[r.last] = 0
					}
				}
				if err != nil {
					return err
				}
			}
			r.lastGPSTime[r.last] += uint64(int64(diff))
		} else if multi == gpstimeMultiCodeFull2 {
			r.next = (r.next + 1) & 3
			hi, err := r.icGPSTime.Decompress(int32(r.lastGPSTime[r.last]>>32), 8)
			if err != nil {
				return err
			}
			r.lastGPSTime[r.next] = uint64(hi) << 32
			lo, err := r.dec.ReadInt()
			if err != nil {
				return err
			}
			r.lastGPSTime[r.next] |= uint64(lo)
			r.last = r.next
			r.lastGPSTimeDiff[r.last] = 0
			r.multiExtremeCounter[r.last] = 0
		} else if multi >= gpstimeMultiCodeFull2 {
			r.last = (r.last + multi - gpstimeMultiCodeFull2) & 3
			return r.Read(item, context)
		}
	}
	binary.LittleEndian.PutUint64(item[:8], r.lastGPSTime[r.last])
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// RGB12 v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASreadItemCompressedRGB12v2 struct {
	dec      *ArithmeticDecoder
	lastItem [3]uint16

	mByteUsed *ArithmeticModel
	mRGBDiff0 *ArithmeticModel
	mRGBDiff1 *ArithmeticModel
	mRGBDiff2 *ArithmeticModel
	mRGBDiff3 *ArithmeticModel
	mRGBDiff4 *ArithmeticModel
	mRGBDiff5 *ArithmeticModel
}

func NewLASreadItemCompressedRGB12v2(dec *ArithmeticDecoder) *LASreadItemCompressedRGB12v2 {
	r := &LASreadItemCompressedRGB12v2{dec: dec}
	r.mByteUsed = dec.CreateSymbolModel(128)
	r.mRGBDiff0 = dec.CreateSymbolModel(256)
	r.mRGBDiff1 = dec.CreateSymbolModel(256)
	r.mRGBDiff2 = dec.CreateSymbolModel(256)
	r.mRGBDiff3 = dec.CreateSymbolModel(256)
	r.mRGBDiff4 = dec.CreateSymbolModel(256)
	r.mRGBDiff5 = dec.CreateSymbolModel(256)
	return r
}

func (r *LASreadItemCompressedRGB12v2) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedRGB12v2) Init(item []byte, _ *uint32) error {
	r.dec.InitSymbolModel(r.mByteUsed, nil)
	r.dec.InitSymbolModel(r.mRGBDiff0, nil)
	r.dec.InitSymbolModel(r.mRGBDiff1, nil)
	r.dec.InitSymbolModel(r.mRGBDiff2, nil)
	r.dec.InitSymbolModel(r.mRGBDiff3, nil)
	r.dec.InitSymbolModel(r.mRGBDiff4, nil)
	r.dec.InitSymbolModel(r.mRGBDiff5, nil)
	r.lastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	r.lastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	r.lastItem[2] = binary.LittleEndian.Uint16(item[4:6])
	return nil
}

func (r *LASreadItemCompressedRGB12v2) Read(item []byte, _ *uint32) error {
	sym, err := r.dec.DecodeSymbol(r.mByteUsed)
	if err != nil {
		return err
	}

	var out [3]uint16 // R, G, B

	if sym&(1<<0) != 0 {
		corr, err := r.dec.DecodeSymbol(r.mRGBDiff0)
		if err != nil {
			return err
		}
		out[0] = uint16(u8Fold(int(int32(corr) + int32(r.lastItem[0]&0xFF))))
	} else {
		out[0] = r.lastItem[0] & 0xFF
	}

	if sym&(1<<1) != 0 {
		corr, err := r.dec.DecodeSymbol(r.mRGBDiff1)
		if err != nil {
			return err
		}
		out[0] |= uint16(u8Fold(int(int32(corr)+int32(r.lastItem[0]>>8)))) << 8
	} else {
		out[0] |= r.lastItem[0] & 0xFF00
	}

	if sym&(1<<6) != 0 {
		// Cross-channel prediction: G and B delta-coded relative to R
		diff := int32(out[0]&0x00FF) - int32(r.lastItem[0]&0x00FF)

		if sym&(1<<2) != 0 {
			corr, err := r.dec.DecodeSymbol(r.mRGBDiff2)
			if err != nil {
				return err
			}
			out[1] = uint16(u8Fold(int(int32(corr) + int32(u8Clamp(diff+int32(r.lastItem[1]&0xFF))))))
		} else {
			out[1] = r.lastItem[1] & 0xFF
		}

		if sym&(1<<4) != 0 {
			corr, err := r.dec.DecodeSymbol(r.mRGBDiff4)
			if err != nil {
				return err
			}
			diff = (diff + (int32(out[1]&0x00FF) - int32(r.lastItem[1]&0x00FF))) / 2
			out[2] = uint16(u8Fold(int(int32(corr) + int32(u8Clamp(diff+int32(r.lastItem[2]&0xFF))))))
		} else {
			out[2] = r.lastItem[2] & 0xFF
		}

		diff = int32(out[0]>>8) - int32(r.lastItem[0]>>8)

		if sym&(1<<3) != 0 {
			corr, err := r.dec.DecodeSymbol(r.mRGBDiff3)
			if err != nil {
				return err
			}
			out[1] |= uint16(u8Fold(int(int32(corr)+int32(u8Clamp(diff+int32(r.lastItem[1]>>8)))))) << 8
		} else {
			out[1] |= r.lastItem[1] & 0xFF00
		}

		if sym&(1<<5) != 0 {
			corr, err := r.dec.DecodeSymbol(r.mRGBDiff5)
			if err != nil {
				return err
			}
			diff = (diff + (int32(out[1]>>8) - int32(r.lastItem[1]>>8))) / 2
			out[2] |= uint16(u8Fold(int(int32(corr)+int32(u8Clamp(diff+int32(r.lastItem[2]>>8)))))) << 8
		} else {
			out[2] |= r.lastItem[2] & 0xFF00
		}
	} else {
		// All channels equal to R
		out[1] = out[0]
		out[2] = out[0]
	}

	r.lastItem = out
	binary.LittleEndian.PutUint16(item[0:2], out[0])
	binary.LittleEndian.PutUint16(item[2:4], out[1])
	binary.LittleEndian.PutUint16(item[4:6], out[2])
	return nil
}

// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .
// BYTE v2
// . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . . .

type LASreadItemCompressedBytev2 struct {
	dec      *ArithmeticDecoder
	number   uint32
	lastItem []byte
	mByte    []*ArithmeticModel
}

func NewLASreadItemCompressedBytev2(dec *ArithmeticDecoder, number uint32) *LASreadItemCompressedBytev2 {
	r := &LASreadItemCompressedBytev2{dec: dec, number: number}
	r.lastItem = make([]byte, number)
	r.mByte = make([]*ArithmeticModel, number)
	for i := range number {
		r.mByte[i] = dec.CreateSymbolModel(256)
	}
	return r
}

func (r *LASreadItemCompressedBytev2) ChunkSizes() error { return nil }

func (r *LASreadItemCompressedBytev2) Init(item []byte, _ *uint32) error {
	for i := uint32(0); i < r.number; i++ {
		r.dec.InitSymbolModel(r.mByte[i], nil)
	}
	copy(r.lastItem, item[:r.number])
	return nil
}

func (r *LASreadItemCompressedBytev2) Read(item []byte, _ *uint32) error {
	for i := uint32(0); i < r.number; i++ {
		sym, err := r.dec.DecodeSymbol(r.mByte[i])
		if err != nil {
			return err
		}
		val := int32(r.lastItem[i]) + int32(sym)
		item[i] = u8Fold(int(val))
	}
	copy(r.lastItem, item[:r.number])
	return nil
}
