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

import (
	"encoding/binary"
	"math"
)

// ---------------------------------------------------------------------------
// POINT14 v4 — layered decompression for LAS 1.4 point types 6-10
//
// Each attribute layer has its own ByteStreamInArray + ArithmeticDecoder.
// The `dec` field is only used to access the main ByteStreamIn (not for
// actual entropy decoding — that goes through per-layer decoders).
// v4 differs only in scanner-channel context-switch logic (fixed bug).
// ---------------------------------------------------------------------------

type LASreadItemCompressedPoint14v4 struct {
	dec *ArithmeticDecoder // only used for instream access, not decoding

	// Per-layer instreams
	instreamChRXY, instreamZ, instreamClass, instreamFlags *ByteStreamInArray
	instreamIntensity, instreamScanAngle, instreamUserData *ByteStreamInArray
	instreamPointSource, instreamGPSTime                   *ByteStreamInArray

	// Per-layer decoders
	decChRXY, decZ, decClass, decFlags      *ArithmeticDecoder
	decIntensity, decScanAngle, decUserData *ArithmeticDecoder
	decPointSource, decGPSTime              *ArithmeticDecoder

	// Changed flags per layer
	changedZ, changedClass, changedFlags, changedIntensity                bool
	changedScanAngle, changedUserData, changedPointSource, changedGPSTime bool

	// Byte counts per layer
	numBytesChRXY, numBytesZ, numBytesClass, numBytesFlags uint32
	numBytesIntensity, numBytesScanAngle, numBytesUserData uint32
	numBytesPointSource, numBytesGPSTime                   uint32

	// Selective decompression
	requestedZ, requestedClass, requestedFlags, requestedIntensity                bool
	requestedScanAngle, requestedUserData, requestedPointSource, requestedGPSTime bool

	// Buffer for reading layer data
	bytes             []byte
	numBytesAllocated uint32

	currentContext uint32
	contexts       [4]LAScontextPOINT14
}

func NewLASreadItemCompressedPoint14v4(dec *ArithmeticDecoder, decompressSelective uint32) *LASreadItemCompressedPoint14v4 {
	r := &LASreadItemCompressedPoint14v4{dec: dec}
	r.requestedZ = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_Z) != 0
	r.requestedClass = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_CLASSIFICATION) != 0
	r.requestedFlags = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_FLAGS) != 0
	r.requestedIntensity = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_INTENSITY) != 0
	r.requestedScanAngle = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_SCAN_ANGLE) != 0
	r.requestedUserData = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_USER_DATA) != 0
	r.requestedPointSource = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_POINT_SOURCE) != 0
	r.requestedGPSTime = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_GPS_TIME) != 0
	for c := range 4 {
		r.contexts[c].MChangedValues[0] = nil
	}
	return r
}

func (r *LASreadItemCompressedPoint14v4) ChunkSizes() error {
	instream := r.dec.GetByteStreamIn()
	buf := make([]byte, 4)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesChRXY = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesZ = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesClass = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesFlags = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesIntensity = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesScanAngle = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesUserData = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesPointSource = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesGPSTime = binary.LittleEndian.Uint32(buf)
	return nil
}

func (r *LASreadItemCompressedPoint14v4) Init(item []byte, ctx *uint32) error {
	instream := r.dec.GetByteStreamIn()

	// Lazy init instreams and decoders (first chunk only)
	if r.instreamChRXY == nil {
		r.instreamChRXY = NewByteStreamInArray(nil)
		r.instreamZ = NewByteStreamInArray(nil)
		r.instreamClass = NewByteStreamInArray(nil)
		r.instreamFlags = NewByteStreamInArray(nil)
		r.instreamIntensity = NewByteStreamInArray(nil)
		r.instreamScanAngle = NewByteStreamInArray(nil)
		r.instreamUserData = NewByteStreamInArray(nil)
		r.instreamPointSource = NewByteStreamInArray(nil)
		r.instreamGPSTime = NewByteStreamInArray(nil)

		r.decChRXY = NewArithmeticDecoder()
		r.decZ = NewArithmeticDecoder()
		r.decClass = NewArithmeticDecoder()
		r.decFlags = NewArithmeticDecoder()
		r.decIntensity = NewArithmeticDecoder()
		r.decScanAngle = NewArithmeticDecoder()
		r.decUserData = NewArithmeticDecoder()
		r.decPointSource = NewArithmeticDecoder()
		r.decGPSTime = NewArithmeticDecoder()
	}

	// Compute total bytes needed
	totalBytes := r.numBytesChRXY
	if r.requestedZ {
		totalBytes += r.numBytesZ
	}
	if r.requestedClass {
		totalBytes += r.numBytesClass
	}
	if r.requestedFlags {
		totalBytes += r.numBytesFlags
	}
	if r.requestedIntensity {
		totalBytes += r.numBytesIntensity
	}
	if r.requestedScanAngle {
		totalBytes += r.numBytesScanAngle
	}
	if r.requestedUserData {
		totalBytes += r.numBytesUserData
	}
	if r.requestedPointSource {
		totalBytes += r.numBytesPointSource
	}
	if r.requestedGPSTime {
		totalBytes += r.numBytesGPSTime
	}

	// Grow buffer if needed
	if totalBytes > r.numBytesAllocated {
		r.bytes = make([]byte, totalBytes)
		r.numBytesAllocated = totalBytes
	}

	// Read all layer data
	off := uint32(0)
	if err := instream.GetBytes(r.bytes[off : off+r.numBytesChRXY]); err != nil {
		return err
	}
	r.instreamChRXY.Init(r.bytes[off : off+r.numBytesChRXY])
	r.decChRXY.Init(r.instreamChRXY, true)
	off += r.numBytesChRXY

	if r.requestedZ {
		if r.numBytesZ > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesZ]); err != nil {
				return err
			}
			r.instreamZ.Init(r.bytes[off : off+r.numBytesZ])
			r.decZ.Init(r.instreamZ, true)
			off += r.numBytesZ
			r.changedZ = true
		} else {
			r.instreamZ.Init(nil)
			r.changedZ = false
		}
	} else {
		if r.numBytesZ > 0 {
			if err := instream.SkipBytes(r.numBytesZ); err != nil {
				return err
			}
		}
		r.changedZ = false
	}
	if r.requestedClass {
		if r.numBytesClass > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesClass]); err != nil {
				return err
			}
			r.instreamClass.Init(r.bytes[off : off+r.numBytesClass])
			r.decClass.Init(r.instreamClass, true)
			off += r.numBytesClass
			r.changedClass = true
		} else {
			r.instreamClass.Init(nil)
			r.changedClass = false
		}
	} else {
		if r.numBytesClass > 0 {
			if err := instream.SkipBytes(r.numBytesClass); err != nil {
				return err
			}
		}
		r.changedClass = false
	}
	if r.requestedFlags {
		if r.numBytesFlags > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesFlags]); err != nil {
				return err
			}
			r.instreamFlags.Init(r.bytes[off : off+r.numBytesFlags])
			r.decFlags.Init(r.instreamFlags, true)
			off += r.numBytesFlags
			r.changedFlags = true
		} else {
			r.instreamFlags.Init(nil)
			r.changedFlags = false
		}
	} else {
		if r.numBytesFlags > 0 {
			if err := instream.SkipBytes(r.numBytesFlags); err != nil {
				return err
			}
		}
		r.changedFlags = false
	}
	if r.requestedIntensity {
		if r.numBytesIntensity > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesIntensity]); err != nil {
				return err
			}
			r.instreamIntensity.Init(r.bytes[off : off+r.numBytesIntensity])
			r.decIntensity.Init(r.instreamIntensity, true)
			off += r.numBytesIntensity
			r.changedIntensity = true
		} else {
			r.instreamIntensity.Init(nil)
			r.changedIntensity = false
		}
	} else {
		if r.numBytesIntensity > 0 {
			if err := instream.SkipBytes(r.numBytesIntensity); err != nil {
				return err
			}
		}
		r.changedIntensity = false
	}
	if r.requestedScanAngle {
		if r.numBytesScanAngle > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesScanAngle]); err != nil {
				return err
			}
			r.instreamScanAngle.Init(r.bytes[off : off+r.numBytesScanAngle])
			r.decScanAngle.Init(r.instreamScanAngle, true)
			off += r.numBytesScanAngle
			r.changedScanAngle = true
		} else {
			r.instreamScanAngle.Init(nil)
			r.changedScanAngle = false
		}
	} else {
		if r.numBytesScanAngle > 0 {
			if err := instream.SkipBytes(r.numBytesScanAngle); err != nil {
				return err
			}
		}
		r.changedScanAngle = false
	}
	if r.requestedUserData {
		if r.numBytesUserData > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesUserData]); err != nil {
				return err
			}
			r.instreamUserData.Init(r.bytes[off : off+r.numBytesUserData])
			r.decUserData.Init(r.instreamUserData, true)
			off += r.numBytesUserData
			r.changedUserData = true
		} else {
			r.instreamUserData.Init(nil)
			r.changedUserData = false
		}
	} else {
		if r.numBytesUserData > 0 {
			if err := instream.SkipBytes(r.numBytesUserData); err != nil {
				return err
			}
		}
		r.changedUserData = false
	}
	if r.requestedPointSource {
		if r.numBytesPointSource > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesPointSource]); err != nil {
				return err
			}
			r.instreamPointSource.Init(r.bytes[off : off+r.numBytesPointSource])
			r.decPointSource.Init(r.instreamPointSource, true)
			off += r.numBytesPointSource
			r.changedPointSource = true
		} else {
			r.instreamPointSource.Init(nil)
			r.changedPointSource = false
		}
	} else {
		if r.numBytesPointSource > 0 {
			if err := instream.SkipBytes(r.numBytesPointSource); err != nil {
				return err
			}
		}
		r.changedPointSource = false
	}
	if r.requestedGPSTime {
		if r.numBytesGPSTime > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesGPSTime]); err != nil {
				return err
			}
			r.instreamGPSTime.Init(r.bytes[off : off+r.numBytesGPSTime])
			r.decGPSTime.Init(r.instreamGPSTime, true)
			off += r.numBytesGPSTime
			r.changedGPSTime = true
		} else {
			r.instreamGPSTime.Init(nil)
			r.changedGPSTime = false
		}
	} else {
		if r.numBytesGPSTime > 0 {
			if err := instream.SkipBytes(r.numBytesGPSTime); err != nil {
				return err
			}
		}
		r.changedGPSTime = false
	}

	// Mark all contexts unused, then set current from item's scanner_channel
	for c := range 4 {
		r.contexts[c].Unused = true
	}
	// item[22] bits 2-3 is scanner_channel in the 40-byte LAStempReadPoint10 output
	// (see readitem_raw.go POINT14 remapping)
	r.currentContext = uint32((item[22] >> 2) & 0x03)
	// C++: context = current_context — propagate to subsequent items' Init calls
	*ctx = r.currentContext

	// Create/init models for current context
	r.createAndInitModelsDecompressors(r.currentContext, item)
	return nil
}

func (r *LASreadItemCompressedPoint14v4) createAndInitModelsDecompressors(ctx uint32, item []byte) {
	c := &r.contexts[ctx]
	if !c.Unused {
		return
	}

	if c.MChangedValues[0] == nil {
		// channel_returns_XY layer
		for i := range 8 {
			c.MChangedValues[i] = r.decChRXY.CreateSymbolModel(128)
		}
		c.MScannerChannel = r.decChRXY.CreateSymbolModel(3)
		c.MReturnNumberGPS = r.decChRXY.CreateSymbolModel(13)
		c.IcDX = NewIntegerDecompressor(r.decChRXY, 32, 2, 8, 0)
		c.IcDY = NewIntegerDecompressor(r.decChRXY, 32, 22, 8, 0)

		// Z layer
		c.IcZ = NewIntegerDecompressor(r.decZ, 32, 20, 8, 0)

		// intensity
		c.IcIntensity = NewIntegerDecompressor(r.decIntensity, 16, 4, 8, 0)
		// scan_angle
		c.IcScanAngle = NewIntegerDecompressor(r.decScanAngle, 16, 2, 8, 0)
		// point_source_ID
		c.IcPointSourceID = NewIntegerDecompressor(r.decPointSource, 16, 1, 8, 0)

		// GPS time
		c.MGPSTimeMulti = r.decGPSTime.CreateSymbolModel(gpstimeMultiTotalV3) // v4: 515 symbols (not 516 as in v2)
		c.MGPSTime0Diff = r.decGPSTime.CreateSymbolModel(5)
		c.IcGPSTime = NewIntegerDecompressor(r.decGPSTime, 32, 9, 8, 0)
	}

	// Init channel_returns_XY models
	for i := range 8 {
		r.decChRXY.InitSymbolModel(c.MChangedValues[i], nil)
	}
	r.decChRXY.InitSymbolModel(c.MScannerChannel, nil)
	for i := range 16 {
		if c.MNumberOfReturns[i] != nil {
			r.decChRXY.InitSymbolModel(c.MNumberOfReturns[i], nil)
		}
		if c.MReturnNumber[i] != nil {
			r.decChRXY.InitSymbolModel(c.MReturnNumber[i], nil)
		}
	}
	r.decChRXY.InitSymbolModel(c.MReturnNumberGPS, nil)
	c.IcDX.InitDecompressor()
	c.IcDY.InitDecompressor()
	for i := range 12 {
		c.LastXDiffMed5[i] = NewStreamingMedian5()
		c.LastYDiffMed5[i] = NewStreamingMedian5()
	}

	// Z layer
	c.IcZ.InitDecompressor()
	// Init last_Z from item (item is 40-byte LAStempReadPoint10, Z at bytes 8-11)
	c.LastZ[0] = int32(binary.LittleEndian.Uint32(item[8:12]))
	for i := 1; i < 8; i++ {
		c.LastZ[i] = c.LastZ[0]
	}

	// classification/flags/user_data (lazy models)
	for i := range 64 {
		if c.MClassification[i] != nil {
			r.decClass.InitSymbolModel(c.MClassification[i], nil)
		}
		if c.MFlags[i] != nil {
			r.decFlags.InitSymbolModel(c.MFlags[i], nil)
		}
		if c.MUserData[i] != nil {
			r.decUserData.InitSymbolModel(c.MUserData[i], nil)
		}
	}

	// intensity
	c.IcIntensity.InitDecompressor()
	for i := range 8 {
		c.LastIntensity[i] = binary.LittleEndian.Uint16(item[12:14])
	}

	// scan_angle
	c.IcScanAngle.InitDecompressor()

	// point_source_ID
	c.IcPointSourceID.InitDecompressor()

	// GPS time
	r.decGPSTime.InitSymbolModel(c.MGPSTimeMulti, nil)
	r.decGPSTime.InitSymbolModel(c.MGPSTime0Diff, nil)
	c.IcGPSTime.InitDecompressor()
	c.Last = 0
	c.Next = 0
	for i := range 4 {
		c.LastGPSTimeDiff[i] = 0
		c.MultiExtremeCounter[i] = 0
	}
	c.LastGPSTime[0] = binary.LittleEndian.Uint64(item[32:40]) // GPS at [32:40] in 40-byte layout
	for i := 1; i < 4; i++ {
		c.LastGPSTime[i] = 0
	}

	// Copy item into LastItem. C++ copies sizeof(LASpoint14) = 128 bytes,
	// but our item is the 40-byte LAStempReadPoint10 layout (raw reader
	// output). The compressed reader only accesses offsets 0–39 from
	// LastItem; offsets 40+ are populated by the separate RGB14 / RGBNIR14
	// items. Zero-fill the unused tail so comparisons are deterministic.
	copy(c.LastItem[:40], item[:40])
	for i := 40; i < 128; i++ {
		c.LastItem[i] = 0
	}
	// C++: last_item->gps_time_change = FALSE — reset carried-over flag from previous chunk
	c.GPSTimeChange = false

	c.Unused = false
}

// Read decompresses all layers for one point.
// C++ original: LASreadItemCompressed_POINT14_v4::read(U8* item, U32& context)
func (r *LASreadItemCompressedPoint14v4) Read(item []byte, context *uint32) error {
	c := &r.contexts[r.currentContext]
	lastItem := c.LastItem[:]

	// ---- channel_returns_XY layer ----

	// Determine last point return context from extended fields.
	// Our 40-byte LAStempReadPoint10 layout:
	//   byte 24: extended_return_number:4 | extended_number_of_returns:4
	//   gps_time_change persisted in context struct (c.GPSTimeChange)
	lastRN := lastItem[24] & 0x0F
	lastNR := (lastItem[24] >> 4) & 0x0F
	gpsTimeChangeFlag := c.GPSTimeChange

	// Build lpr context: first? (bit 0), last? (bit 1), gps_change? (bit 2)
	lpr := uint32(0)
	if lastRN == 1 {
		lpr += 1 // first
	}
	if lastRN >= lastNR {
		lpr += 2 // last
	}
	if gpsTimeChangeFlag {
		lpr += 4 // gps_time_change
	}

	// Decode changed_values mask
	changedValues, err := r.decChRXY.DecodeSymbol(c.MChangedValues[lpr])
	if err != nil {
		return err
	}

	// Scanner channel switch (bit 6)
	if changedValues&(1<<6) != 0 {
		diff, err := r.decChRXY.DecodeSymbol(c.MScannerChannel)
		if err != nil {
			return err
		}
		newSC := (r.currentContext + diff + 1) % 4
		if r.contexts[newSC].Unused {
			r.createAndInitModelsDecompressors(newSC, lastItem)
		}
		r.currentContext = newSC
		c = &r.contexts[r.currentContext]
		lastItem = c.LastItem[:]
		// Persist scanner_channel in byte 22 bits 2-3 of saved context
		lastItem[22] = (lastItem[22] & 0xF3) | byte(newSC<<2)
		// Refresh from new context's last item (C++ re-reads last_item fields after switch)
		lastRN = lastItem[24] & 0x0F
		lastNR = (lastItem[24] >> 4) & 0x0F
	}

	// Determine sub-change flags (correct C++ bit positions)
	gpsTimeChange := (changedValues & (1 << 4)) != 0     // bit 4
	pointSourceChange := (changedValues & (1 << 5)) != 0 // bit 5
	scanAngleChange := (changedValues & (1 << 3)) != 0   // bit 3

	// Number of returns change (bit 2)
	n := uint32(lastNR)
	if changedValues&(1<<2) != 0 {
		if c.MNumberOfReturns[lastNR] == nil {
			c.MNumberOfReturns[lastNR] = r.decChRXY.CreateSymbolModel(16)
			r.decChRXY.InitSymbolModel(c.MNumberOfReturns[lastNR], nil)
		}
		nv, err := r.decChRXY.DecodeSymbol(c.MNumberOfReturns[lastNR])
		if err != nil {
			return err
		}
		n = nv
		lastItem[24] = (lastItem[24] & 0x0F) | byte(nv<<4) // number_of_returns
	}

	// Return number change (bits 0-1)
	rVal := uint32(lastRN)
	switch changedValues & 3 {
	case 0:
		rVal = uint32(lastRN) // same
	case 1:
		rVal = (uint32(lastRN) + 1) % 16 // plus 1
		lastItem[24] = (lastItem[24] & 0xF0) | byte(rVal)
	case 2:
		rVal = (uint32(lastRN) + 15) % 16 // minus 1
		lastItem[24] = (lastItem[24] & 0xF0) | byte(rVal)
	case 3:
		if gpsTimeChange {
			if c.MReturnNumber[lastRN] == nil {
				c.MReturnNumber[lastRN] = r.decChRXY.CreateSymbolModel(16)
				r.decChRXY.InitSymbolModel(c.MReturnNumber[lastRN], nil)
			}
			nv, err := r.decChRXY.DecodeSymbol(c.MReturnNumber[lastRN])
			if err != nil {
				return err
			}
			rVal = nv
		} else {
			sym, err := r.decChRXY.DecodeSymbol(c.MReturnNumberGPS)
			if err != nil {
				return err
			}
			rVal = (uint32(lastRN) + (sym + 2)) % 16
		}
		lastItem[24] = (lastItem[24] & 0xF0) | byte(rVal)
	}

	// Set legacy return counts in byte 14 (3-bit fields)
	var legacyRN, legacyNR uint8
	if n > 7 {
		if rVal > 6 {
			if rVal >= n {
				legacyRN = 7
			} else {
				legacyRN = 6
			}
		} else {
			legacyRN = uint8(rVal)
		}
		legacyNR = 7
	} else {
		legacyRN = uint8(rVal)
		legacyNR = uint8(n)
	}
	// Mask to 3 bits like the C++ bitfield assignment truncates: for
	// out-of-spec data (extended return number > 7 with number of returns
	// <= 7) legacyRN would otherwise spill into the legacyNR bits.
	lastItem[14] = (lastItem[14] & 0xC0) | (legacyRN & 0x07) | ((legacyNR & 0x07) << 3)

	// Get return map m (6-context) and level l (8-context)
	m := uint32(NumberReturnMap6ctx[n][rVal])
	l := uint32(NumberReturnLevel8ctx[n][rVal])

	// cpr: current point return context (single/first/last/intermediate)
	cpr := uint32(0)
	if rVal == 1 {
		cpr += 2 // first
	}
	if rVal >= n {
		cpr += 1 // last
	}

	// Intensity / scan_angle prediction uses CURRENT point's gps_time_change
	// (from changedValues), not the previous point's flag.
	gpsCtx := uint32(0)
	if gpsTimeChange {
		gpsCtx = 1
	}

	// ---- decompress X ----
	medX := c.LastXDiffMed5[(m<<1)|gpsCtx].Get()
	xDiff, err := c.IcDX.Decompress(medX, boolU32(n == 1))
	if err != nil {
		return err
	}
	curX := int32(binary.LittleEndian.Uint32(lastItem[0:4]))
	curX += xDiff
	binary.LittleEndian.PutUint32(lastItem[0:4], uint32(curX))
	c.LastXDiffMed5[(m<<1)|gpsCtx].Add(xDiff)

	// ---- decompress Y ----
	medY := c.LastYDiffMed5[(m<<1)|gpsCtx].Get()
	kBits := c.IcDX.GetK()
	yCtx := boolU32(n == 1)
	if kBits < 20 {
		yCtx = yCtx + u32ZeroBit0(kBits)
	} else {
		yCtx = yCtx + 20
	}
	yDiff, err := c.IcDY.Decompress(medY, yCtx)
	if err != nil {
		return err
	}
	curY := int32(binary.LittleEndian.Uint32(lastItem[4:8]))
	curY += yDiff
	binary.LittleEndian.PutUint32(lastItem[4:8], uint32(curY))
	c.LastYDiffMed5[(m<<1)|gpsCtx].Add(yDiff)

	// ---- decompress Z (if changed and requested) ----
	if r.changedZ {
		kBits := (c.IcDX.GetK() + c.IcDY.GetK()) / 2
		zCtx := boolU32(n == 1)
		if kBits < 18 {
			zCtx += u32ZeroBit0(kBits)
		} else {
			zCtx += 18
		}
		zVal, err := c.IcZ.Decompress(c.LastZ[l], zCtx)
		if err != nil {
			return err
		}
		binary.LittleEndian.PutUint32(lastItem[8:12], uint32(zVal))
		c.LastZ[l] = int32(zVal)
	}

	// ---- decompress classification (if changed and requested) ----
	if r.changedClass {
		lastCls := lastItem[23] // extended classification
		// C++ context: ((classification & 0x1F) << 1) + (cpr == 3 ? 1 : 0)
		ccc := (lastCls & 0x1F) << 1
		if cpr == 3 {
			ccc++
		}
		if c.MClassification[ccc] == nil {
			c.MClassification[ccc] = r.decClass.CreateSymbolModel(256)
			r.decClass.InitSymbolModel(c.MClassification[ccc], nil)
		}
		newCls, err := r.decClass.DecodeSymbol(c.MClassification[ccc])
		if err != nil {
			return err
		}
		lastItem[23] = byte(newCls)

		// Update legacy classification in byte 15
		if newCls < 32 {
			lastItem[15] = (lastItem[15] & 0xE0) | byte(newCls)
		} else {
			lastItem[15] = lastItem[15] & 0xE0 // legacy_classification = 0
		}
	}

	// ---- decompress flags (if changed and requested) ----
	if r.changedFlags {
		// C++ flags context: eofl:1<<5 | scan_dir:1<<4 | classification_flags:4
		eofl := (lastItem[14] >> 7) & 0x01
		scandir := (lastItem[14] >> 6) & 0x01
		clsFlags := (lastItem[22] >> 4) & 0x0F
		lastFlagsIdx := (eofl << 5) | (scandir << 4) | clsFlags
		if c.MFlags[lastFlagsIdx] == nil {
			c.MFlags[lastFlagsIdx] = r.decFlags.CreateSymbolModel(64)
			r.decFlags.InitSymbolModel(c.MFlags[lastFlagsIdx], nil)
		}
		flags, err := r.decFlags.DecodeSymbol(c.MFlags[lastFlagsIdx])
		if err != nil {
			return err
		}
		// Store back: eofl at bit 7, scan_dir at bit 6 of byte 14
		newEofl := uint8((flags >> 5) & 1)
		newScandir := uint8((flags >> 4) & 1)
		newClsFlags := uint8(flags & 0x0F)
		lastItem[14] = (lastItem[14] & 0x3F) | (newEofl << 7) | (newScandir << 6)
		// classification_flags go into byte 22 bits 4-7
		lastItem[22] = (lastItem[22] & 0x0F) | (newClsFlags << 4)
		// legacy_flags into byte 15 bits 5-7 (same byte as legacy_classification
		// in C++ LASpoint14 struct; classification in bits 0-4, flags in bits 5-7)
		lastItem[15] = (lastItem[15] & 0x1F) | ((uint8(flags) & 0x07) << 5)
	}

	// ---- decompress intensity (if changed and requested) ----
	if r.changedIntensity {
		idx := (cpr << 1) | gpsCtx
		val, err := c.IcIntensity.Decompress(int32(c.LastIntensity[idx]), cpr)
		if err != nil {
			return err
		}
		c.LastIntensity[idx] = uint16(val)
		binary.LittleEndian.PutUint16(lastItem[12:14], uint16(val))
	}

	// ---- decompress scan_angle (if changed and requested) ----
	if r.changedScanAngle {
		if scanAngleChange {
			sa := int16(binary.LittleEndian.Uint16(lastItem[20:22]))
			val, err := c.IcScanAngle.Decompress(int32(sa), gpsCtx)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(lastItem[20:22], uint16(int16(val)))
			// Compute legacy scan_angle_rank = I8_CLAMP(I16_QUANTIZE(0.006f * scan_angle))
			sa = int16(val)
			saf := float32(sa) * 0.006
			var saClamped int32
			if saf >= 0 {
				saClamped = int32(saf + 0.5)
			} else {
				saClamped = int32(saf - 0.5)
			}
			lastItem[16] = byte(i32ClampI8(saClamped))
		}
	}

	// ---- decompress user_data (if changed and requested) ----
	if r.changedUserData {
		ud := lastItem[17]
		udIdx := ud / 4
		if c.MUserData[udIdx] == nil {
			c.MUserData[udIdx] = r.decUserData.CreateSymbolModel(256)
			r.decUserData.InitSymbolModel(c.MUserData[udIdx], nil)
		}
		newUD, err := r.decUserData.DecodeSymbol(c.MUserData[udIdx])
		if err != nil {
			return err
		}
		lastItem[17] = byte(newUD)
	}

	// ---- decompress point_source (if changed and requested) ----
	if r.changedPointSource {
		if pointSourceChange {
			val, err := c.IcPointSourceID.Decompress(int32(binary.LittleEndian.Uint16(lastItem[18:20])), 0)
			if err != nil {
				return err
			}
			binary.LittleEndian.PutUint16(lastItem[18:20], uint16(val))
		}
	}

	// ---- decompress GPS time (if changed and requested) ----
	if r.changedGPSTime {
		if gpsTimeChange {
			if err := r.readGPSTime(c); err != nil {
				return err
			}
			// readGPSTime writes to c.LastItem[32:40] internally
		}
	}

	// Save deleted_flag from incoming item, then copy lastItem→item, then restore
	oldDeleted := binary.LittleEndian.Uint32(item[28:32])
	copy(item[:40], lastItem[:40])
	binary.LittleEndian.PutUint32(item[28:32], oldDeleted)
	// Persist gps_time_change for next point in context struct (not in output buffer)
	c.GPSTimeChange = gpsTimeChange
	// C++: context = current_context; // the POINT14 reader sets context for all other items
	*context = r.currentContext
	return nil
}

func (r *LASreadItemCompressedPoint14v4) readGPSTime(c *LAScontextPOINT14) error {
	last := c.Last
	if c.LastGPSTimeDiff[last] == 0 {
		multi, err := r.decGPSTime.DecodeSymbol(c.MGPSTime0Diff)
		if err != nil {
			return err
		}
		if multi == 0 {
			// the difference can be represented with 32 bits
			c.LastGPSTimeDiff[last], err = c.IcGPSTime.Decompress(0, 0)
			if err != nil {
				return err
			}
			c.LastGPSTime[last] += uint64(int64(c.LastGPSTimeDiff[last]))
			c.MultiExtremeCounter[last] = 0
		} else if multi == 1 {
			// the difference is huge
			c.Next = (c.Next + 1) & 3
			hi, err := c.IcGPSTime.Decompress(int32(c.LastGPSTime[last]>>32), 8)
			if err != nil {
				return err
			}
			c.LastGPSTime[c.Next] = uint64(hi) << 32
			lo, err := r.decGPSTime.ReadInt()
			if err != nil {
				return err
			}
			c.LastGPSTime[c.Next] |= uint64(lo)
			c.Last = c.Next
			c.LastGPSTimeDiff[c.Last] = 0
			c.MultiExtremeCounter[c.Last] = 0
		} else {
			// switch to another sequence
			c.Last = (c.Last + multi - 1) & 3
			return r.readGPSTime(c)
		}
	} else {
		multi, err := r.decGPSTime.DecodeSymbol(c.MGPSTimeMulti)
		if err != nil {
			return err
		}
		if multi == 1 {
			diff, err := c.IcGPSTime.Decompress(c.LastGPSTimeDiff[last], 1)
			if err != nil {
				return err
			}
			c.LastGPSTime[last] += uint64(int64(diff))
			c.MultiExtremeCounter[last] = 0
		} else if multi < gpstimeMultiCodeFullV3 {
			var diff int32
			if multi == 0 {
				diff, err = c.IcGPSTime.Decompress(0, 7)
				if err != nil {
					return err
				}
				c.MultiExtremeCounter[last]++
				if c.MultiExtremeCounter[last] > 3 {
					c.LastGPSTimeDiff[last] = diff
					c.MultiExtremeCounter[last] = 0
				}
			} else if multi < uint32(gpstimeMulti2) {
				if multi < 10 {
					diff, err = c.IcGPSTime.Decompress(int32(multi)*c.LastGPSTimeDiff[last], 2)
				} else {
					diff, err = c.IcGPSTime.Decompress(int32(multi)*c.LastGPSTimeDiff[last], 3)
				}
				if err != nil {
					return err
				}
			} else if multi == uint32(gpstimeMulti2) {
				diff, err = c.IcGPSTime.Decompress(int32(gpstimeMulti2)*c.LastGPSTimeDiff[last], 4)
				if err != nil {
					return err
				}
				c.MultiExtremeCounter[last]++
				if c.MultiExtremeCounter[last] > 3 {
					c.LastGPSTimeDiff[last] = diff
					c.MultiExtremeCounter[last] = 0
				}
			} else {
				multi2 := int32(gpstimeMulti2) - int32(multi)
				if multi2 > int32(gpstimeMultiMinus2) {
					diff, err = c.IcGPSTime.Decompress(multi2*c.LastGPSTimeDiff[last], 5)
				} else {
					diff, err = c.IcGPSTime.Decompress(int32(gpstimeMultiMinus2)*c.LastGPSTimeDiff[last], 6)
					if err != nil {
						return err
					}
					c.MultiExtremeCounter[last]++
					if c.MultiExtremeCounter[last] > 3 {
						c.LastGPSTimeDiff[last] = diff
						c.MultiExtremeCounter[last] = 0
					}
				}
				if err != nil {
					return err
				}
			}
			c.LastGPSTime[last] += uint64(int64(diff))
		} else if multi == gpstimeMultiCodeFullV3 {
			c.Next = (c.Next + 1) & 3
			hi, err := c.IcGPSTime.Decompress(int32(c.LastGPSTime[last]>>32), 8)
			if err != nil {
				return err
			}
			c.LastGPSTime[c.Next] = uint64(hi) << 32
			lo, err := r.decGPSTime.ReadInt()
			if err != nil {
				return err
			}
			c.LastGPSTime[c.Next] |= uint64(lo)
			c.Last = c.Next
			c.LastGPSTimeDiff[c.Last] = 0
			c.MultiExtremeCounter[c.Last] = 0
		} else if multi > gpstimeMultiCodeFullV3 {
			c.Last = (c.Last + multi - gpstimeMultiCodeFullV3) & 3
			return r.readGPSTime(c)
		}
	}
	// Write GPS time to bytes 32-39 of lastItem (40-byte LAStempReadPoint10 layout)
	binary.LittleEndian.PutUint64(c.LastItem[32:40], c.LastGPSTime[c.Last])
	return nil
}

// ---------------------------------------------------------------------------
// RGB14 v4 — layered decompression for LAS 1.4 RGB (3×uint16)
//
// Each context has its own set of 7 symbol models for byte-level prediction.
// The `dec` field is only used to access the main ByteStreamIn.
// Context reads: item is a 6-byte buffer (R, G, B as three uint16 LE).
// ---------------------------------------------------------------------------

type LASreadItemCompressedRGB14v4 struct {
	dec *ArithmeticDecoder

	instreamRGB *ByteStreamInArray
	decRGB      *ArithmeticDecoder

	changedRGB bool

	numBytesRGB uint32

	requestedRGB bool

	bytes             []byte
	numBytesAllocated uint32

	currentContext uint32
	contexts       [4]LAScontextRGB14
}

func NewLASreadItemCompressedRGB14v4(dec *ArithmeticDecoder, decompressSelective uint32) *LASreadItemCompressedRGB14v4 {
	r := &LASreadItemCompressedRGB14v4{dec: dec}
	r.requestedRGB = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_RGB) != 0
	for c := range 4 {
		r.contexts[c].MByteUsed = nil
	}
	return r
}

func (r *LASreadItemCompressedRGB14v4) ChunkSizes() error {
	instream := r.dec.GetByteStreamIn()
	buf := make([]byte, 4)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesRGB = binary.LittleEndian.Uint32(buf)
	return nil
}

func (r *LASreadItemCompressedRGB14v4) Init(item []byte, ctx *uint32) error {
	instream := r.dec.GetByteStreamIn()

	// Lazy init instream and decoder (first chunk only)
	if r.instreamRGB == nil {
		r.instreamRGB = NewByteStreamInArray(nil)
		r.decRGB = NewArithmeticDecoder()
	}

	// Handle RGB layer
	if r.requestedRGB {
		if r.numBytesRGB > 0 {
			if r.numBytesRGB > r.numBytesAllocated {
				r.bytes = make([]byte, r.numBytesRGB)
				r.numBytesAllocated = r.numBytesRGB
			}
			if err := instream.GetBytes(r.bytes[:r.numBytesRGB]); err != nil {
				return err
			}
			r.instreamRGB.Init(r.bytes[:r.numBytesRGB])
			r.decRGB.Init(r.instreamRGB, true)
			r.changedRGB = true
		} else {
			r.instreamRGB.Init(nil)
			r.changedRGB = false
		}
	} else {
		if r.numBytesRGB > 0 {
			if err := instream.SkipBytes(r.numBytesRGB); err != nil {
				return err
			}
		}
		r.changedRGB = false
	}

	// Mark all contexts unused, then init current context from POINT14
	for c := range 4 {
		r.contexts[c].Unused = true
	}
	r.currentContext = *ctx
	r.createAndInitModelsDecompressors(r.currentContext, item)
	return nil
}

func (r *LASreadItemCompressedRGB14v4) createAndInitModelsDecompressors(ctx uint32, item []byte) {
	c := &r.contexts[ctx]
	if !c.Unused {
		return
	}

	if c.MByteUsed == nil {
		c.MByteUsed = r.decRGB.CreateSymbolModel(128)
		c.MRGBDiff0 = r.decRGB.CreateSymbolModel(256)
		c.MRGBDiff1 = r.decRGB.CreateSymbolModel(256)
		c.MRGBDiff2 = r.decRGB.CreateSymbolModel(256)
		c.MRGBDiff3 = r.decRGB.CreateSymbolModel(256)
		c.MRGBDiff4 = r.decRGB.CreateSymbolModel(256)
		c.MRGBDiff5 = r.decRGB.CreateSymbolModel(256)
	}

	r.decRGB.InitSymbolModel(c.MByteUsed, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff0, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff1, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff2, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff3, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff4, nil)
	r.decRGB.InitSymbolModel(c.MRGBDiff5, nil)

	c.LastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	c.LastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	c.LastItem[2] = binary.LittleEndian.Uint16(item[4:6])

	c.Unused = false
}

// Read decompresses one RGB point (6 bytes = 3×uint16 LE).
// C++ original: LASreadItemCompressed_RGB14_v4::read(U8* item, U32& context)
func (r *LASreadItemCompressedRGB14v4) Read(item []byte, context *uint32) error {
	c := &r.contexts[r.currentContext]
	lastItem := c.LastItem[:]

	if r.currentContext != *context {
		var seed [6]byte
		binary.LittleEndian.PutUint16(seed[0:2], lastItem[0])
		binary.LittleEndian.PutUint16(seed[2:4], lastItem[1])
		binary.LittleEndian.PutUint16(seed[4:6], lastItem[2])
		r.currentContext = *context
		if r.contexts[r.currentContext].Unused {
			r.createAndInitModelsDecompressors(r.currentContext, seed[:])
		}
		c = &r.contexts[r.currentContext]
		lastItem = c.LastItem[:]
	}

	if !r.changedRGB {
		binary.LittleEndian.PutUint16(item[0:2], lastItem[0])
		binary.LittleEndian.PutUint16(item[2:4], lastItem[1])
		binary.LittleEndian.PutUint16(item[4:6], lastItem[2])
		return nil
	}

	sym, err := r.decRGB.DecodeSymbol(c.MByteUsed)
	if err != nil {
		return err
	}

	if sym&(1<<0) != 0 {
		corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff0)
		if err != nil {
			return err
		}
		item[0] = u8Fold(int(corr) + int(lastItem[0]&0xFF))
	} else {
		item[0] = uint8(lastItem[0] & 0xFF)
	}

	if sym&(1<<1) != 0 {
		corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff1)
		if err != nil {
			return err
		}
		item[1] = u8Fold(int(corr) + int(lastItem[0]>>8))
	} else {
		item[1] = uint8(lastItem[0] >> 8)
	}

	U16 := func(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }

	if sym&(1<<6) != 0 {
		diff := int(U16(item[0:2])&0x00FF) - int(lastItem[0]&0x00FF)
		if sym&(1<<2) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff2)
			if err != nil {
				return err
			}
			item[2] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[1]&0xFF)))))
		} else {
			item[2] = uint8(lastItem[1] & 0xFF)
		}
		if sym&(1<<4) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff4)
			if err != nil {
				return err
			}
			diff = (diff + (int(U16(item[2:4])&0x00FF) - int(lastItem[1]&0x00FF))) / 2
			item[4] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[2]&0xFF)))))
		} else {
			item[4] = uint8(lastItem[2] & 0xFF)
		}
		diff = int(U16(item[0:2])>>8) - int(lastItem[0]>>8)
		if sym&(1<<3) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff3)
			if err != nil {
				return err
			}
			item[3] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[1]>>8)))))
		} else {
			item[3] = uint8(lastItem[1] >> 8)
		}
		if sym&(1<<5) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff5)
			if err != nil {
				return err
			}
			diff = (diff + (int(U16(item[2:4])>>8) - int(lastItem[1]>>8))) / 2
			item[5] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[2]>>8)))))
		} else {
			item[5] = uint8(lastItem[2] >> 8)
		}
	} else {
		copy(item[2:4], item[0:2])
		copy(item[4:6], item[0:2])
	}

	lastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	lastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	lastItem[2] = binary.LittleEndian.Uint16(item[4:6])

	return nil
}

// ---------------------------------------------------------------------------
// RGBNIR14 v4 — layered decompression for LAS 1.4 RGB+NIR (4×uint16)
//
// Two separate layers: RGB (6 bytes, same as RGB14) and NIR (2 bytes).
// Each has its own instream, decoder, and model set.
// ---------------------------------------------------------------------------

type LASreadItemCompressedRGBNIR14v4 struct {
	dec *ArithmeticDecoder

	instreamRGB *ByteStreamInArray
	instreamNIR *ByteStreamInArray
	decRGB      *ArithmeticDecoder
	decNIR      *ArithmeticDecoder

	changedRGB bool
	changedNIR bool

	numBytesRGB uint32
	numBytesNIR uint32

	requestedRGB bool
	requestedNIR bool

	bytes             []byte
	numBytesAllocated uint32

	currentContext uint32
	contexts       [4]LAScontextRGBNIR14
}

func NewLASreadItemCompressedRGBNIR14v4(dec *ArithmeticDecoder, decompressSelective uint32) *LASreadItemCompressedRGBNIR14v4 {
	r := &LASreadItemCompressedRGBNIR14v4{dec: dec}
	r.requestedRGB = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_RGB) != 0
	r.requestedNIR = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_NIR) != 0
	for c := range 4 {
		r.contexts[c].MRGBBytesUsed = nil
		r.contexts[c].MNIRBytesUsed = nil
	}
	return r
}

func (r *LASreadItemCompressedRGBNIR14v4) ChunkSizes() error {
	instream := r.dec.GetByteStreamIn()
	buf := make([]byte, 4)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesRGB = binary.LittleEndian.Uint32(buf)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesNIR = binary.LittleEndian.Uint32(buf)
	return nil
}

func (r *LASreadItemCompressedRGBNIR14v4) Init(item []byte, ctx *uint32) error {
	instream := r.dec.GetByteStreamIn()

	// Lazy init instreams and decoders (first chunk only)
	if r.instreamRGB == nil {
		r.instreamRGB = NewByteStreamInArray(nil)
		r.instreamNIR = NewByteStreamInArray(nil)
		r.decRGB = NewArithmeticDecoder()
		r.decNIR = NewArithmeticDecoder()
	}

	// Compute total bytes needed
	totalBytes := uint32(0)
	if r.requestedRGB {
		totalBytes += r.numBytesRGB
	}
	if r.requestedNIR {
		totalBytes += r.numBytesNIR
	}

	// Grow buffer if needed
	if totalBytes > r.numBytesAllocated {
		r.bytes = make([]byte, totalBytes)
		r.numBytesAllocated = totalBytes
	}

	// Read RGB layer
	off := uint32(0)
	if r.requestedRGB {
		if r.numBytesRGB > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesRGB]); err != nil {
				return err
			}
			r.instreamRGB.Init(r.bytes[off : off+r.numBytesRGB])
			r.decRGB.Init(r.instreamRGB, true)
			off += r.numBytesRGB
			r.changedRGB = true
		} else {
			r.instreamRGB.Init(nil)
			r.changedRGB = false
		}
	} else {
		if r.numBytesRGB > 0 {
			if err := instream.SkipBytes(r.numBytesRGB); err != nil {
				return err
			}
		}
		r.changedRGB = false
	}

	// Read NIR layer
	if r.requestedNIR {
		if r.numBytesNIR > 0 {
			if err := instream.GetBytes(r.bytes[off : off+r.numBytesNIR]); err != nil {
				return err
			}
			r.instreamNIR.Init(r.bytes[off : off+r.numBytesNIR])
			r.decNIR.Init(r.instreamNIR, true)
			r.changedNIR = true
		} else {
			r.instreamNIR.Init(nil)
			r.changedNIR = false
		}
	} else {
		if r.numBytesNIR > 0 {
			if err := instream.SkipBytes(r.numBytesNIR); err != nil {
				return err
			}
		}
		r.changedNIR = false
	}

	// Mark all contexts unused, then init current context from POINT14
	for c := range 4 {
		r.contexts[c].Unused = true
	}
	r.currentContext = *ctx
	r.createAndInitModelsDecompressors(r.currentContext, item)
	return nil
}

func (r *LASreadItemCompressedRGBNIR14v4) createAndInitModelsDecompressors(ctx uint32, item []byte) {
	c := &r.contexts[ctx]
	if !c.Unused {
		return
	}

	// RGB models (same as RGB14)
	if r.requestedRGB {
		if c.MRGBBytesUsed == nil {
			c.MRGBBytesUsed = r.decRGB.CreateSymbolModel(128)
			c.MRGBDiff0 = r.decRGB.CreateSymbolModel(256)
			c.MRGBDiff1 = r.decRGB.CreateSymbolModel(256)
			c.MRGBDiff2 = r.decRGB.CreateSymbolModel(256)
			c.MRGBDiff3 = r.decRGB.CreateSymbolModel(256)
			c.MRGBDiff4 = r.decRGB.CreateSymbolModel(256)
			c.MRGBDiff5 = r.decRGB.CreateSymbolModel(256)
		}
		r.decRGB.InitSymbolModel(c.MRGBBytesUsed, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff0, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff1, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff2, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff3, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff4, nil)
		r.decRGB.InitSymbolModel(c.MRGBDiff5, nil)
	}

	// NIR models
	if r.requestedNIR {
		if c.MNIRBytesUsed == nil {
			c.MNIRBytesUsed = r.decNIR.CreateSymbolModel(4)
			c.MNIRDiff0 = r.decNIR.CreateSymbolModel(256)
			c.MNIRDiff1 = r.decNIR.CreateSymbolModel(256)
		}
		r.decNIR.InitSymbolModel(c.MNIRBytesUsed, nil)
		r.decNIR.InitSymbolModel(c.MNIRDiff0, nil)
		r.decNIR.InitSymbolModel(c.MNIRDiff1, nil)
	}

	// Copy 8-byte item (RGB 6 + NIR 2) into last_item as seed
	c.LastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	c.LastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	c.LastItem[2] = binary.LittleEndian.Uint16(item[4:6])
	c.LastItem[3] = binary.LittleEndian.Uint16(item[6:8])

	c.Unused = false
}

// Read decompresses one RGBNIR point (8 bytes = 4×uint16 LE).
// C++ original: LASreadItemCompressed_RGBNIR14_v4::read(U8* item, U32& context)
func (r *LASreadItemCompressedRGBNIR14v4) Read(item []byte, context *uint32) error {
	c := &r.contexts[r.currentContext]
	lastItem := c.LastItem[:]

	if r.currentContext != *context {
		var seed [8]byte
		binary.LittleEndian.PutUint16(seed[0:2], lastItem[0])
		binary.LittleEndian.PutUint16(seed[2:4], lastItem[1])
		binary.LittleEndian.PutUint16(seed[4:6], lastItem[2])
		binary.LittleEndian.PutUint16(seed[6:8], lastItem[3])
		r.currentContext = *context
		if r.contexts[r.currentContext].Unused {
			r.createAndInitModelsDecompressors(r.currentContext, seed[:])
		}
		c = &r.contexts[r.currentContext]
		lastItem = c.LastItem[:]
	}

	// ---- decompress RGB layer (identical to RGB14) ----

	if r.changedRGB {
		sym, err := r.decRGB.DecodeSymbol(c.MRGBBytesUsed)
		if err != nil {
			return err
		}

		if sym&(1<<0) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff0)
			if err != nil {
				return err
			}
			item[0] = u8Fold(int(corr) + int(lastItem[0]&0xFF))
		} else {
			item[0] = uint8(lastItem[0] & 0xFF)
		}

		if sym&(1<<1) != 0 {
			corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff1)
			if err != nil {
				return err
			}
			item[1] = u8Fold(int(corr) + int(lastItem[0]>>8))
		} else {
			item[1] = uint8(lastItem[0] >> 8)
		}

		U16 := func(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }

		if sym&(1<<6) != 0 {
			diff := int(U16(item[0:2])&0x00FF) - int(lastItem[0]&0x00FF)
			if sym&(1<<2) != 0 {
				corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff2)
				if err != nil {
					return err
				}
				item[2] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[1]&0xFF)))))
			} else {
				item[2] = uint8(lastItem[1] & 0xFF)
			}
			if sym&(1<<4) != 0 {
				corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff4)
				if err != nil {
					return err
				}
				diff = (diff + (int(U16(item[2:4])&0x00FF) - int(lastItem[1]&0x00FF))) / 2
				item[4] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[2]&0xFF)))))
			} else {
				item[4] = uint8(lastItem[2] & 0xFF)
			}
			diff = int(U16(item[0:2])>>8) - int(lastItem[0]>>8)
			if sym&(1<<3) != 0 {
				corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff3)
				if err != nil {
					return err
				}
				item[3] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[1]>>8)))))
			} else {
				item[3] = uint8(lastItem[1] >> 8)
			}
			if sym&(1<<5) != 0 {
				corr, err := r.decRGB.DecodeSymbol(c.MRGBDiff5)
				if err != nil {
					return err
				}
				diff = (diff + (int(U16(item[2:4])>>8) - int(lastItem[1]>>8))) / 2
				item[5] = u8Fold(int(corr) + int(u8Clamp(int32(diff+int(lastItem[2]>>8)))))
			} else {
				item[5] = uint8(lastItem[2] >> 8)
			}
		} else {
			copy(item[2:4], item[0:2])
			copy(item[4:6], item[0:2])
		}

		lastItem[0] = binary.LittleEndian.Uint16(item[0:2])
		lastItem[1] = binary.LittleEndian.Uint16(item[2:4])
		lastItem[2] = binary.LittleEndian.Uint16(item[4:6])
	} else {
		binary.LittleEndian.PutUint16(item[0:2], lastItem[0])
		binary.LittleEndian.PutUint16(item[2:4], lastItem[1])
		binary.LittleEndian.PutUint16(item[4:6], lastItem[2])
	}

	// ---- decompress NIR layer ----

	if r.changedNIR {
		sym, err := r.decNIR.DecodeSymbol(c.MNIRBytesUsed)
		if err != nil {
			return err
		}

		if sym&(1<<0) != 0 {
			corr, err := r.decNIR.DecodeSymbol(c.MNIRDiff0)
			if err != nil {
				return err
			}
			item[6] = u8Fold(int(corr) + int(lastItem[3]&0xFF))
		} else {
			item[6] = uint8(lastItem[3] & 0xFF)
		}
		if sym&(1<<1) != 0 {
			corr, err := r.decNIR.DecodeSymbol(c.MNIRDiff1)
			if err != nil {
				return err
			}
			item[7] = u8Fold(int(corr) + int(lastItem[3]>>8))
		} else {
			item[7] = uint8(lastItem[3] >> 8)
		}
		lastItem[3] = binary.LittleEndian.Uint16(item[6:8])
	} else {
		binary.LittleEndian.PutUint16(item[6:8], lastItem[3])
	}

	return nil
}

// ---------------------------------------------------------------------------
// WAVEPACKET14 v4 — layered decompression for LAS 1.4 wave packet (29 bytes)
//
// Single layer with 4 symbol models + 4 integer compressors.
// Context: LAScontextWAVEPACKET14
// ---------------------------------------------------------------------------

type LASreadItemCompressedWavepacket14v4 struct {
	dec *ArithmeticDecoder

	instreamWavepacket *ByteStreamInArray
	decWavepacket      *ArithmeticDecoder

	changedWavepacket bool

	numBytesWavepacket uint32

	requestedWavepacket bool

	bytes             []byte
	numBytesAllocated uint32

	currentContext uint32
	contexts       [4]LAScontextWAVEPACKET14
}

func NewLASreadItemCompressedWavepacket14v4(dec *ArithmeticDecoder, decompressSelective uint32) *LASreadItemCompressedWavepacket14v4 {
	r := &LASreadItemCompressedWavepacket14v4{dec: dec}
	r.requestedWavepacket = (decompressSelective & LASZIP_DECOMPRESS_SELECTIVE_WAVEPACKET) != 0
	for c := range 4 {
		r.contexts[c].MPacketIndex = nil
	}
	return r
}

func (r *LASreadItemCompressedWavepacket14v4) ChunkSizes() error {
	instream := r.dec.GetByteStreamIn()
	buf := make([]byte, 4)
	if err := instream.Get32bitsLE(buf); err != nil {
		return err
	}
	r.numBytesWavepacket = binary.LittleEndian.Uint32(buf)
	return nil
}

func (r *LASreadItemCompressedWavepacket14v4) Init(item []byte, ctx *uint32) error {
	instream := r.dec.GetByteStreamIn()

	// Lazy init instream and decoder (first chunk only)
	if r.instreamWavepacket == nil {
		r.instreamWavepacket = NewByteStreamInArray(nil)
		r.decWavepacket = NewArithmeticDecoder()
	}

	// Grow buffer if needed
	if r.numBytesWavepacket > r.numBytesAllocated {
		r.bytes = make([]byte, r.numBytesWavepacket)
		r.numBytesAllocated = r.numBytesWavepacket
	}

	// Handle wavepacket layer
	if r.requestedWavepacket {
		if r.numBytesWavepacket > 0 {
			if err := instream.GetBytes(r.bytes[:r.numBytesWavepacket]); err != nil {
				return err
			}
			r.instreamWavepacket.Init(r.bytes[:r.numBytesWavepacket])
			r.decWavepacket.Init(r.instreamWavepacket, true)
			r.changedWavepacket = true
		} else {
			r.instreamWavepacket.Init(nil)
			r.changedWavepacket = false
		}
	} else {
		if r.numBytesWavepacket > 0 {
			if err := instream.SkipBytes(r.numBytesWavepacket); err != nil {
				return err
			}
		}
		r.changedWavepacket = false
	}

	// Mark all contexts unused, then init current context from POINT14
	for c := range 4 {
		r.contexts[c].Unused = true
	}
	r.currentContext = *ctx
	r.createAndInitModelsDecompressors(r.currentContext, item)
	return nil
}

func (r *LASreadItemCompressedWavepacket14v4) createAndInitModelsDecompressors(ctx uint32, item []byte) {
	c := &r.contexts[ctx]
	if !c.Unused {
		return
	}

	if r.requestedWavepacket {
		if c.MPacketIndex == nil {
			c.MPacketIndex = r.decWavepacket.CreateSymbolModel(256)
			c.MOffsetDiff[0] = r.decWavepacket.CreateSymbolModel(4)
			c.MOffsetDiff[1] = r.decWavepacket.CreateSymbolModel(4)
			c.MOffsetDiff[2] = r.decWavepacket.CreateSymbolModel(4)
			c.MOffsetDiff[3] = r.decWavepacket.CreateSymbolModel(4)
			c.IcOffsetDiff = NewIntegerDecompressor(r.decWavepacket, 32, 1, 8, 0)
			c.IcPacketSize = NewIntegerDecompressor(r.decWavepacket, 32, 1, 8, 0)
			c.IcReturnPoint = NewIntegerDecompressor(r.decWavepacket, 32, 1, 8, 0)
			c.IcXYZ = NewIntegerDecompressor(r.decWavepacket, 32, 3, 8, 0)
		}

		r.decWavepacket.InitSymbolModel(c.MPacketIndex, nil)
		r.decWavepacket.InitSymbolModel(c.MOffsetDiff[0], nil)
		r.decWavepacket.InitSymbolModel(c.MOffsetDiff[1], nil)
		r.decWavepacket.InitSymbolModel(c.MOffsetDiff[2], nil)
		r.decWavepacket.InitSymbolModel(c.MOffsetDiff[3], nil)
		c.IcOffsetDiff.InitDecompressor()
		c.IcPacketSize.InitDecompressor()
		c.IcReturnPoint.InitDecompressor()
		c.IcXYZ.InitDecompressor()
	}

	c.LastDiff32 = 0
	c.SymLastOffsetDiff = 0
	copy(c.LastItem[:29], item[:29])

	c.Unused = false
}

// Read decompresses one WAVEPACKET point (29 bytes).
// C++ original: LASreadItemCompressed_WAVEPACKET14_v4::read(U8* item, U32& context)
func (r *LASreadItemCompressedWavepacket14v4) Read(item []byte, context *uint32) error {
	c := &r.contexts[r.currentContext]
	lastItem := c.LastItem[:]

	if r.currentContext != *context {
		r.currentContext = *context
		if r.contexts[r.currentContext].Unused {
			r.createAndInitModelsDecompressors(r.currentContext, lastItem[:29])
		}
		c = &r.contexts[r.currentContext]
		lastItem = c.LastItem[:]
	}

	if !r.changedWavepacket {
		copy(item[:29], lastItem[:29])
		return nil
	}

	// Byte 0: packet index
	pkt, err := r.decWavepacket.DecodeSymbol(c.MPacketIndex)
	if err != nil {
		return err
	}
	item[0] = byte(pkt)

	// Bytes 1-28: wave packet body (same as WAVEPACKET13)
	lastWP := UnpackLASwavepacket13(lastItem[1:])

	c.SymLastOffsetDiff, err = r.decWavepacket.DecodeSymbol(c.MOffsetDiff[c.SymLastOffsetDiff])
	if err != nil {
		return err
	}

	var wp LASwavepacket13
	switch c.SymLastOffsetDiff {
	case 0:
		wp.Offset = lastWP.Offset
	case 1:
		wp.Offset = lastWP.Offset + uint64(lastWP.PacketSize)
	case 2:
		c.LastDiff32, err = c.IcOffsetDiff.Decompress(c.LastDiff32, 0)
		if err != nil {
			return err
		}
		wp.Offset = lastWP.Offset + uint64(c.LastDiff32)
	default:
		wp.Offset, err = r.decWavepacket.ReadInt64()
		if err != nil {
			return err
		}
	}

	ps, err := c.IcPacketSize.Decompress(int32(lastWP.PacketSize), 0)
	if err != nil {
		return err
	}
	wp.PacketSize = uint32(ps)

	rp, err := c.IcReturnPoint.Decompress(int32(math.Float32bits(lastWP.ReturnPoint)), 0)
	if err != nil {
		return err
	}
	wp.ReturnPoint = math.Float32frombits(uint32(rp))

	rx, err := c.IcXYZ.Decompress(int32(math.Float32bits(lastWP.X)), 0)
	if err != nil {
		return err
	}
	wp.X = math.Float32frombits(uint32(rx))

	ry, err := c.IcXYZ.Decompress(int32(math.Float32bits(lastWP.Y)), 1)
	if err != nil {
		return err
	}
	wp.Y = math.Float32frombits(uint32(ry))

	rz, err := c.IcXYZ.Decompress(int32(math.Float32bits(lastWP.Z)), 2)
	if err != nil {
		return err
	}
	wp.Z = math.Float32frombits(uint32(rz))

	// Pack wave packet body into bytes 1-28
	packed := PackLASwavepacket13(&wp)
	copy(item[1:29], packed[:28])

	copy(lastItem[:29], item[:29])
	return nil
}

// ---------------------------------------------------------------------------
// BYTE14 v4 — layered decompression for LAS 1.4 extra bytes (variable count)
//
// One instream/decoder per byte. Each byte gets its own byte-level model.
// Context: LAScontextBYTE14
// ---------------------------------------------------------------------------

type LASreadItemCompressedByte14v4 struct {
	dec *ArithmeticDecoder

	number uint32

	instreamBytes []*ByteStreamInArray
	decBytes      []*ArithmeticDecoder

	changedBytes   []bool
	numBytesBytes  []uint32
	requestedBytes []bool

	bytes             []byte
	numBytesAllocated uint32

	currentContext uint32
	contexts       [4]LAScontextBYTE14
}

func NewLASreadItemCompressedByte14v4(dec *ArithmeticDecoder, number uint32, decompressSelective uint32) *LASreadItemCompressedByte14v4 {
	r := &LASreadItemCompressedByte14v4{dec: dec, number: number}
	r.instreamBytes = make([]*ByteStreamInArray, number)
	r.decBytes = make([]*ArithmeticDecoder, number)
	r.numBytesBytes = make([]uint32, number)
	r.changedBytes = make([]bool, number)
	r.requestedBytes = make([]bool, number)
	for i := range number {
		if i > 15 {
			r.requestedBytes[i] = true
		} else {
			r.requestedBytes[i] = (decompressSelective & (LASZIP_DECOMPRESS_SELECTIVE_BYTE0 << i)) != 0
		}
	}
	for c := range 4 {
		r.contexts[c].MBytes = nil
	}
	return r
}

func (r *LASreadItemCompressedByte14v4) ChunkSizes() error {
	instream := r.dec.GetByteStreamIn()
	buf := make([]byte, 4)
	for i := uint32(0); i < r.number; i++ {
		if err := instream.Get32bitsLE(buf); err != nil {
			return err
		}
		r.numBytesBytes[i] = binary.LittleEndian.Uint32(buf)
	}
	return nil
}

func (r *LASreadItemCompressedByte14v4) Init(item []byte, ctx *uint32) error {
	instream := r.dec.GetByteStreamIn()

	// Lazy init instreams and decoders (first chunk only)
	if len(r.instreamBytes) == 0 || r.instreamBytes[0] == nil {
		r.instreamBytes = make([]*ByteStreamInArray, r.number)
		r.decBytes = make([]*ArithmeticDecoder, r.number)
		for i := uint32(0); i < r.number; i++ {
			r.instreamBytes[i] = NewByteStreamInArray(nil)
			r.decBytes[i] = NewArithmeticDecoder()
		}
	}

	// Compute total bytes needed
	totalBytes := uint32(0)
	for i := uint32(0); i < r.number; i++ {
		if r.requestedBytes[i] {
			totalBytes += r.numBytesBytes[i]
		}
	}

	// Grow buffer if needed
	if totalBytes > r.numBytesAllocated {
		r.bytes = make([]byte, totalBytes)
		r.numBytesAllocated = totalBytes
	}

	// Read all requested byte layers
	off := uint32(0)
	for i := uint32(0); i < r.number; i++ {
		if r.requestedBytes[i] {
			if r.numBytesBytes[i] > 0 {
				if err := instream.GetBytes(r.bytes[off : off+r.numBytesBytes[i]]); err != nil {
					return err
				}
				r.instreamBytes[i].Init(r.bytes[off : off+r.numBytesBytes[i]])
				r.decBytes[i].Init(r.instreamBytes[i], true)
				off += r.numBytesBytes[i]
				r.changedBytes[i] = true
			} else {
				r.decBytes[i].Init(nil, false)
				r.changedBytes[i] = false
			}
		} else {
			if r.numBytesBytes[i] > 0 {
				if err := instream.SkipBytes(r.numBytesBytes[i]); err != nil {
					return err
				}
			}
			r.changedBytes[i] = false
		}
	}

	// Mark all contexts unused, then init current context from POINT14
	for c := range 4 {
		r.contexts[c].Unused = true
	}
	r.currentContext = *ctx
	r.createAndInitModelsDecompressors(r.currentContext, item)
	return nil
}

func (r *LASreadItemCompressedByte14v4) createAndInitModelsDecompressors(ctx uint32, item []byte) {
	c := &r.contexts[ctx]
	if !c.Unused {
		return
	}

	if c.MBytes == nil {
		c.MBytes = make([]*ArithmeticModel, r.number)
		for i := uint32(0); i < r.number; i++ {
			c.MBytes[i] = r.decBytes[i].CreateSymbolModel(256)
		}
		c.LastItem = make([]uint8, r.number)
	}

	// Re-init all models (every chunk)
	for i := uint32(0); i < r.number; i++ {
		r.decBytes[i].InitSymbolModel(c.MBytes[i], nil)
	}

	// Copy seed item
	copy(c.LastItem, item[:r.number])

	c.Unused = false
}

// Read decompresses one BYTE point (number bytes).
// C++ original: LASreadItemCompressed_BYTE14_v4::read(U8* item, U32& context)
func (r *LASreadItemCompressedByte14v4) Read(item []byte, context *uint32) error {
	c := &r.contexts[r.currentContext]
	lastItem := c.LastItem

	if r.currentContext != *context {
		r.currentContext = *context
		if r.contexts[r.currentContext].Unused {
			r.createAndInitModelsDecompressors(r.currentContext, lastItem)
		}
		c = &r.contexts[r.currentContext]
		lastItem = c.LastItem
	}

	for i := uint32(0); i < r.number; i++ {
		if r.changedBytes[i] {
			sym, err := r.decBytes[i].DecodeSymbol(c.MBytes[i])
			if err != nil {
				return err
			}
			item[i] = u8Fold(int(lastItem[i]) + int(sym))
			lastItem[i] = item[i]
		} else {
			item[i] = lastItem[i]
		}
	}
	return nil
}
