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

// writeitem_compressed_v3.go — v3 layered (LAYERED_CHUNKED) item writers,
// ported from src/laswriteitemcompressed_v3.cpp. Strict mirror images of the
// readers in readitem_compressed_v3.go.
//
// The v4 writers (writeitem_compressed_v4.go) differ from v3 only in the
// scanner-channel context propagation semantics, so both versions share the
// unexported *v34Writer cores defined here, gated by a `v4 bool` flag:
//
//   - POINT14: v3 sets the shared *context ONLY inside the scanner-channel-
//     changed branch (C++ laswriteitemcompressed_v3.cpp:576-578); v4 sets it
//     unconditionally after that branch (v4:576).
//   - RGB14/RGBNIR14/WAVEPACKET14/BYTE14: on a context switch, v3 rebinds
//     last_item to the new context only when that context was unused (the
//     "stale last_item" behavior, v3:1285-1293); v4 rebinds unconditionally.
//
// The `enc` passed to the constructors is a dummy encoder that only provides
// access to the main output stream (GetByteStreamOut); actual entropy coding
// goes through per-layer encoders bound to in-memory ByteStreamOutArrays.
package laz

import (
	"bytes"
	"encoding/binary"
	"math"
)

// ---------------------------------------------------------------------------
// POINT14 v3/v4 — layered compression for LAS 1.4 point types 6-10
//
// Nine attribute layers (channel_returns_XY, Z, classification, flags,
// intensity, scan_angle, user_data, point_source, gps_time), each with its
// own ByteStreamOutArray + ArithmeticEncoder.
// ---------------------------------------------------------------------------

type point14v34Writer struct {
	enc *ArithmeticEncoder // only used for main outstream access, not encoding
	v4  bool               // v4 context-propagation semantics

	// Per-layer outstreams
	outChRXY, outZ, outClass, outFlags      *ByteStreamOutArray
	outIntensity, outScanAngle, outUserData *ByteStreamOutArray
	outPointSource, outGPSTime              *ByteStreamOutArray

	// Per-layer encoders
	encChRXY, encZ, encClass, encFlags      *ArithmeticEncoder
	encIntensity, encScanAngle, encUserData *ArithmeticEncoder
	encPointSource, encGPSTime              *ArithmeticEncoder

	// Changed flags per layer (channel_returns_XY and Z are always written)
	changedClass, changedFlags, changedIntensity, changedScanAngle bool
	changedUserData, changedPointSource, changedGPSTime            bool

	currentContext uint32
	contexts       [4]LAScontextPOINT14
}

func newPoint14v34Writer(enc *ArithmeticEncoder, v4 bool) *point14v34Writer {
	return &point14v34Writer{enc: enc, v4: v4}
}

// Init seeds the per-chunk state from the chunk's first (raw) point.
// C++ original: LASwriteItemCompressed_POINT14_v3::init(const U8* item, U32& context)
func (w *point14v34Writer) Init(item []byte, ctx *uint32) error {
	// On the first init create outstreams and encoders; afterwards just reset
	// the layer buffers (Go equivalent of the C++ seek(0) reuse).
	if w.outChRXY == nil {
		w.outChRXY = NewByteStreamOutArray()
		w.outZ = NewByteStreamOutArray()
		w.outClass = NewByteStreamOutArray()
		w.outFlags = NewByteStreamOutArray()
		w.outIntensity = NewByteStreamOutArray()
		w.outScanAngle = NewByteStreamOutArray()
		w.outUserData = NewByteStreamOutArray()
		w.outPointSource = NewByteStreamOutArray()
		w.outGPSTime = NewByteStreamOutArray()

		w.encChRXY = NewArithmeticEncoder()
		w.encZ = NewArithmeticEncoder()
		w.encClass = NewArithmeticEncoder()
		w.encFlags = NewArithmeticEncoder()
		w.encIntensity = NewArithmeticEncoder()
		w.encScanAngle = NewArithmeticEncoder()
		w.encUserData = NewArithmeticEncoder()
		w.encPointSource = NewArithmeticEncoder()
		w.encGPSTime = NewArithmeticEncoder()
	} else {
		w.outChRXY.Reset()
		w.outZ.Reset()
		w.outClass.Reset()
		w.outFlags.Reset()
		w.outIntensity.Reset()
		w.outScanAngle.Reset()
		w.outUserData.Reset()
		w.outPointSource.Reset()
		w.outGPSTime.Reset()
	}

	// Init layer encoders
	if err := w.encChRXY.Init(w.outChRXY); err != nil {
		return err
	}
	if err := w.encZ.Init(w.outZ); err != nil {
		return err
	}
	if err := w.encClass.Init(w.outClass); err != nil {
		return err
	}
	if err := w.encFlags.Init(w.outFlags); err != nil {
		return err
	}
	if err := w.encIntensity.Init(w.outIntensity); err != nil {
		return err
	}
	if err := w.encScanAngle.Init(w.outScanAngle); err != nil {
		return err
	}
	if err := w.encUserData.Init(w.outUserData); err != nil {
		return err
	}
	if err := w.encPointSource.Init(w.outPointSource); err != nil {
		return err
	}
	if err := w.encGPSTime.Init(w.outGPSTime); err != nil {
		return err
	}

	// Set changed booleans to false
	w.changedClass = false
	w.changedFlags = false
	w.changedIntensity = false
	w.changedScanAngle = false
	w.changedUserData = false
	w.changedPointSource = false
	w.changedGPSTime = false

	// Mark the four scanner channel contexts as unused
	for c := range 4 {
		w.contexts[c].Unused = true
	}

	// Set scanner channel as current context.
	// item[22] bits 2-3 is scanner_channel in the 40-byte in-memory layout
	// (see readitem_raw.go POINT14 remapping).
	w.currentContext = uint32((item[22] >> 2) & 0x03)
	// C++: context = current_context — the POINT14 writer sets context for
	// all other items' Init calls.
	*ctx = w.currentContext

	w.createAndInitModelsAndCompressors(w.currentContext, item)
	return nil
}

// createAndInitModelsAndCompressors mirrors the reader's
// createAndInitModelsDecompressors model-for-model.
func (w *point14v34Writer) createAndInitModelsAndCompressors(ctx uint32, item []byte) {
	c := &w.contexts[ctx]

	// First create all entropy models and integer compressors (if needed)
	if c.MChangedValues[0] == nil {
		// channel_returns_XY layer
		for i := range 8 {
			c.MChangedValues[i] = w.encChRXY.CreateSymbolModel(128)
		}
		c.MScannerChannel = w.encChRXY.CreateSymbolModel(3)
		c.MReturnNumberGPS = w.encChRXY.CreateSymbolModel(13)
		c.IcDX = NewIntegerCompressor(w.encChRXY, 32, 2, 8, 0)
		c.IcDY = NewIntegerCompressor(w.encChRXY, 32, 22, 8, 0)

		// Z layer
		c.IcZ = NewIntegerCompressor(w.encZ, 32, 20, 8, 0)

		// intensity
		c.IcIntensity = NewIntegerCompressor(w.encIntensity, 16, 4, 8, 0)
		// scan_angle
		c.IcScanAngle = NewIntegerCompressor(w.encScanAngle, 16, 2, 8, 0)
		// point_source_ID
		c.IcPointSourceID = NewIntegerCompressor(w.encPointSource, 16, 1, 8, 0)

		// GPS time
		c.MGPSTimeMulti = w.encGPSTime.CreateSymbolModel(gpstimeMultiTotalV3)
		c.MGPSTime0Diff = w.encGPSTime.CreateSymbolModel(5)
		c.IcGPSTime = NewIntegerCompressor(w.encGPSTime, 32, 9, 8, 0)
	}

	// Init channel_returns_XY models
	for i := range 8 {
		w.encChRXY.InitSymbolModel(c.MChangedValues[i], nil)
	}
	w.encChRXY.InitSymbolModel(c.MScannerChannel, nil)
	for i := range 16 {
		if c.MNumberOfReturns[i] != nil {
			w.encChRXY.InitSymbolModel(c.MNumberOfReturns[i], nil)
		}
		if c.MReturnNumber[i] != nil {
			w.encChRXY.InitSymbolModel(c.MReturnNumber[i], nil)
		}
	}
	w.encChRXY.InitSymbolModel(c.MReturnNumberGPS, nil)
	c.IcDX.InitCompressor()
	c.IcDY.InitCompressor()
	for i := range 12 {
		c.LastXDiffMed5[i] = NewStreamingMedian5()
		c.LastYDiffMed5[i] = NewStreamingMedian5()
	}

	// Z layer: init last_Z from item (Z at bytes 8-11)
	c.IcZ.InitCompressor()
	lastZ := int32(binary.LittleEndian.Uint32(item[8:12]))
	for i := range 8 {
		c.LastZ[i] = lastZ
	}

	// classification/flags/user_data (lazy models)
	for i := range 64 {
		if c.MClassification[i] != nil {
			w.encClass.InitSymbolModel(c.MClassification[i], nil)
		}
		if c.MFlags[i] != nil {
			w.encFlags.InitSymbolModel(c.MFlags[i], nil)
		}
		if c.MUserData[i] != nil {
			w.encUserData.InitSymbolModel(c.MUserData[i], nil)
		}
	}

	// intensity
	c.IcIntensity.InitCompressor()
	intensity := binary.LittleEndian.Uint16(item[12:14])
	for i := range 8 {
		c.LastIntensity[i] = intensity
	}

	// scan_angle
	c.IcScanAngle.InitCompressor()

	// point_source_ID
	c.IcPointSourceID.InitCompressor()

	// GPS time
	w.encGPSTime.InitSymbolModel(c.MGPSTimeMulti, nil)
	w.encGPSTime.InitSymbolModel(c.MGPSTime0Diff, nil)
	c.IcGPSTime.InitCompressor()
	c.Last = 0
	c.Next = 0
	for i := range 4 {
		c.LastGPSTimeDiff[i] = 0
		c.MultiExtremeCounter[i] = 0
	}
	c.LastGPSTime[0] = binary.LittleEndian.Uint64(item[32:40]) // GPS at [32:40]
	for i := 1; i < 4; i++ {
		c.LastGPSTime[i] = 0
	}

	// Init current context from item. The reader only accesses offsets 0-39
	// of LastItem; zero-fill the tail for deterministic state.
	copy(c.LastItem[:40], item[:40])
	for i := 40; i < 128; i++ {
		c.LastItem[i] = 0
	}
	// C++: last_item->gps_time_change = FALSE
	c.GPSTimeChange = false

	c.Unused = false
}

// Write compresses one point into the nine layers.
// C++ original: LASwriteItemCompressed_POINT14_v3::write(const U8* item, U32& context)
func (w *point14v34Writer) Write(item []byte, context *uint32) error {
	c := &w.contexts[w.currentContext]
	lastItem := c.LastItem[:]

	// ---- channel_returns_XY layer ----

	// Create single (3) / first (1) / last (2) / intermediate (0) context
	// from the LAST point return, plus its gps_time_change flag.
	lastRN := uint32(lastItem[24] & 0x0F)
	lastNR := uint32((lastItem[24] >> 4) & 0x0F)
	lpr := uint32(0)
	if lastRN == 1 {
		lpr += 1 // first
	}
	if lastRN >= lastNR {
		lpr += 2 // last
	}
	if c.GPSTimeChange {
		lpr += 4 // gps_time_change of last point
	}

	// Get the (potentially new) context
	scannerChannel := uint32((item[22] >> 2) & 0x03)

	// If context has changed (and the new context already exists) get last
	// for the new context.
	if scannerChannel != w.currentContext && !w.contexts[scannerChannel].Unused {
		lastItem = w.contexts[scannerChannel].LastItem[:]
	}

	// Determine changed attributes (vs last point of the *target* channel)
	pointSourceChange := binary.LittleEndian.Uint16(item[18:20]) != binary.LittleEndian.Uint16(lastItem[18:20])
	gpsTimeChange := math.Float64frombits(binary.LittleEndian.Uint64(item[32:40])) !=
		math.Float64frombits(binary.LittleEndian.Uint64(lastItem[32:40]))
	scanAngleChange := binary.LittleEndian.Uint16(item[20:22]) != binary.LittleEndian.Uint16(lastItem[20:22])

	// Last and current return counts
	lastN := uint32((lastItem[24] >> 4) & 0x0F)
	lastR := uint32(lastItem[24] & 0x0F)
	n := uint32((item[24] >> 4) & 0x0F)
	rVal := uint32(item[24] & 0x0F)

	// Build the 7-bit changed_values mask
	changedValues := boolU32(scannerChannel != w.currentContext) << 6
	changedValues |= boolU32(pointSourceChange) << 5
	changedValues |= boolU32(gpsTimeChange) << 4
	changedValues |= boolU32(scanAngleChange) << 3
	changedValues |= boolU32(n != lastN) << 2
	if rVal != lastR {
		switch {
		case rVal == (lastR+1)%16:
			changedValues |= 1
		case rVal == (lastR+15)%16:
			changedValues |= 2
		default:
			changedValues |= 3
		}
	}

	// Compress the mask with the last point return context
	w.encChRXY.EncodeSymbol(c.MChangedValues[lpr], changedValues)

	// If scanner channel has changed, record the change
	if changedValues&(1<<6) != 0 {
		diff := int32(scannerChannel) - int32(w.currentContext)
		if diff > 0 {
			w.encChRXY.EncodeSymbol(c.MScannerChannel, uint32(diff-1)) // curr = last + (sym + 1)
		} else {
			w.encChRXY.EncodeSymbol(c.MScannerChannel, uint32(diff+4-1)) // curr = (last + (sym + 1)) % 4
		}
		if w.contexts[scannerChannel].Unused {
			// Create and init entropy models and integer compressors,
			// seeding the new context FROM THE CURRENT context's last item.
			w.createAndInitModelsAndCompressors(scannerChannel, c.LastItem[:])
			lastItem = w.contexts[scannerChannel].LastItem[:]
		}
		// Switch context to the current scanner channel
		w.currentContext = scannerChannel
		if !w.v4 {
			// v3 propagates the context to the other layered items ONLY when
			// the scanner channel changed (C++ v3.cpp:576-578). The caller
			// resets context to 0 for every point.
			*context = w.currentContext
		}
		c = &w.contexts[w.currentContext]
	}
	if w.v4 {
		// v4 fix: unconditionally propagate (C++ v4.cpp:576)
		*context = w.currentContext
	}

	// If number of returns is different we compress it
	if changedValues&(1<<2) != 0 {
		if c.MNumberOfReturns[lastN] == nil {
			c.MNumberOfReturns[lastN] = w.encChRXY.CreateSymbolModel(16)
			w.encChRXY.InitSymbolModel(c.MNumberOfReturns[lastN], nil)
		}
		w.encChRXY.EncodeSymbol(c.MNumberOfReturns[lastN], n)
	}

	// If return number difference is bigger than +1 / -1 we compress how
	if changedValues&3 == 3 {
		if gpsTimeChange {
			// If the GPS time has changed
			if c.MReturnNumber[lastR] == nil {
				c.MReturnNumber[lastR] = w.encChRXY.CreateSymbolModel(16)
				w.encChRXY.InitSymbolModel(c.MReturnNumber[lastR], nil)
			}
			w.encChRXY.EncodeSymbol(c.MReturnNumber[lastR], rVal)
		} else {
			// If the GPS time has not changed
			diff := int32(rVal) - int32(lastR)
			if diff > 1 {
				w.encChRXY.EncodeSymbol(c.MReturnNumberGPS, uint32(diff-2)) // r = last_r + (sym + 2)
			} else {
				w.encChRXY.EncodeSymbol(c.MReturnNumberGPS, uint32(diff+16-2)) // r = (last_r + (sym + 2)) % 16
			}
		}
	}

	// Get return map m and return level l context for current point
	m := uint32(NumberReturnMap6ctx[n][rVal])
	l := uint32(NumberReturnLevel8ctx[n][rVal])

	// Create single (3) / first (1) / last (2) / intermediate (0) return
	// context for current point
	cpr := uint32(0)
	if rVal == 1 {
		cpr += 2 // first
	}
	if rVal >= n {
		cpr += 1 // last
	}

	gpsCtx := boolU32(gpsTimeChange)

	// ---- compress X ----
	medX := c.LastXDiffMed5[(m<<1)|gpsCtx].Get()
	diffX := int32(binary.LittleEndian.Uint32(item[0:4])) - int32(binary.LittleEndian.Uint32(lastItem[0:4]))
	if err := c.IcDX.Compress(medX, diffX, boolU32(n == 1)); err != nil {
		return err
	}
	c.LastXDiffMed5[(m<<1)|gpsCtx].Add(diffX)

	// ---- compress Y ----
	kBits := c.IcDX.GetK()
	medY := c.LastYDiffMed5[(m<<1)|gpsCtx].Get()
	diffY := int32(binary.LittleEndian.Uint32(item[4:8])) - int32(binary.LittleEndian.Uint32(lastItem[4:8]))
	yCtx := boolU32(n == 1)
	if kBits < 20 {
		yCtx += u32ZeroBit0(kBits)
	} else {
		yCtx += 20
	}
	if err := c.IcDY.Compress(medY, diffY, yCtx); err != nil {
		return err
	}
	c.LastYDiffMed5[(m<<1)|gpsCtx].Add(diffY)

	// ---- compress Z layer ----
	kBits = (c.IcDX.GetK() + c.IcDY.GetK()) / 2
	zCtx := boolU32(n == 1)
	if kBits < 18 {
		zCtx += u32ZeroBit0(kBits)
	} else {
		zCtx += 18
	}
	itemZ := int32(binary.LittleEndian.Uint32(item[8:12]))
	if err := c.IcZ.Compress(c.LastZ[l], itemZ, zCtx); err != nil {
		return err
	}
	c.LastZ[l] = itemZ

	// ---- compress classification layer ----
	lastCls := uint32(lastItem[23])
	cls := uint32(item[23])
	if cls != lastCls {
		w.changedClass = true
	}
	ccc := ((lastCls & 0x1F) << 1) + boolU32(cpr == 3)
	if c.MClassification[ccc] == nil {
		c.MClassification[ccc] = w.encClass.CreateSymbolModel(256)
		w.encClass.InitSymbolModel(c.MClassification[ccc], nil)
	}
	w.encClass.EncodeSymbol(c.MClassification[ccc], cls)

	// ---- compress flags layer ----
	lastFlags := (uint32((lastItem[14]>>7)&1) << 5) | (uint32((lastItem[14]>>6)&1) << 4) | uint32((lastItem[22]>>4)&0x0F)
	flags := (uint32((item[14]>>7)&1) << 5) | (uint32((item[14]>>6)&1) << 4) | uint32((item[22]>>4)&0x0F)
	if flags != lastFlags {
		w.changedFlags = true
	}
	if c.MFlags[lastFlags] == nil {
		c.MFlags[lastFlags] = w.encFlags.CreateSymbolModel(64)
		w.encFlags.InitSymbolModel(c.MFlags[lastFlags], nil)
	}
	w.encFlags.EncodeSymbol(c.MFlags[lastFlags], flags)

	// ---- compress intensity layer ----
	itemIntensity := binary.LittleEndian.Uint16(item[12:14])
	if itemIntensity != binary.LittleEndian.Uint16(lastItem[12:14]) {
		w.changedIntensity = true
	}
	intensityIdx := (cpr << 1) | gpsCtx
	if err := c.IcIntensity.Compress(int32(c.LastIntensity[intensityIdx]), int32(itemIntensity), cpr); err != nil {
		return err
	}
	c.LastIntensity[intensityIdx] = itemIntensity

	// ---- compress scan_angle layer (only if changed) ----
	if scanAngleChange {
		w.changedScanAngle = true
		lastSA := int32(int16(binary.LittleEndian.Uint16(lastItem[20:22])))
		itemSA := int32(int16(binary.LittleEndian.Uint16(item[20:22])))
		if err := c.IcScanAngle.Compress(lastSA, itemSA, gpsCtx); err != nil {
			return err
		}
	}

	// ---- compress user_data layer ----
	if item[17] != lastItem[17] {
		w.changedUserData = true
	}
	udIdx := lastItem[17] / 4
	if c.MUserData[udIdx] == nil {
		c.MUserData[udIdx] = w.encUserData.CreateSymbolModel(256)
		w.encUserData.InitSymbolModel(c.MUserData[udIdx], nil)
	}
	w.encUserData.EncodeSymbol(c.MUserData[udIdx], uint32(item[17]))

	// ---- compress point_source layer (only if changed) ----
	if pointSourceChange {
		w.changedPointSource = true
		lastPS := int32(binary.LittleEndian.Uint16(lastItem[18:20]))
		itemPS := int32(binary.LittleEndian.Uint16(item[18:20]))
		if err := c.IcPointSourceID.Compress(lastPS, itemPS, 0); err != nil {
			return err
		}
	}

	// ---- compress gps_time layer (only if changed) ----
	if gpsTimeChange {
		w.changedGPSTime = true
		if err := w.writeGPSTime(c, binary.LittleEndian.Uint64(item[32:40])); err != nil {
			return err
		}
	}

	// Copy the last item and remember if this point had a gps_time_change
	copy(lastItem[:40], item[:40])
	c.GPSTimeChange = gpsTimeChange

	return nil
}

// writeGPSTime is the port of write_gps_time (C++ v3.cpp:975). gpsTime is
// the raw IEEE 754 bit pattern of the F64 GPS time; all arithmetic happens
// on the reinterpreted integer bits exactly like the C++ U64I64F64 union.
func (w *point14v34Writer) writeGPSTime(c *LAScontextPOINT14, gpsTime uint64) error {
	if c.LastGPSTimeDiff[c.Last] == 0 {
		// If the last integer difference was zero
		currDiff64 := int64(gpsTime) - int64(c.LastGPSTime[c.Last])
		currDiff32 := int32(currDiff64)
		if currDiff64 == int64(currDiff32) {
			// The difference can be represented with 32 bits
			w.encGPSTime.EncodeSymbol(c.MGPSTime0Diff, 0)
			if err := c.IcGPSTime.Compress(0, currDiff32, 0); err != nil {
				return err
			}
			c.LastGPSTimeDiff[c.Last] = currDiff32
			c.MultiExtremeCounter[c.Last] = 0
		} else {
			// The difference is huge; maybe it belongs to another sequence
			for i := uint32(1); i < 4; i++ {
				otherDiff64 := int64(gpsTime) - int64(c.LastGPSTime[(c.Last+i)&3])
				if otherDiff64 == int64(int32(otherDiff64)) {
					w.encGPSTime.EncodeSymbol(c.MGPSTime0Diff, i+1) // it belongs to another sequence
					c.Last = (c.Last + i) & 3
					return w.writeGPSTime(c, gpsTime)
				}
			}
			// No other sequence found. Start a new sequence.
			w.encGPSTime.EncodeSymbol(c.MGPSTime0Diff, 1)
			if err := c.IcGPSTime.Compress(int32(c.LastGPSTime[c.Last]>>32), int32(gpsTime>>32), 8); err != nil {
				return err
			}
			w.encGPSTime.WriteInt(uint32(gpsTime))
			c.Next = (c.Next + 1) & 3
			c.Last = c.Next
			c.LastGPSTimeDiff[c.Last] = 0
			c.MultiExtremeCounter[c.Last] = 0
		}
		c.LastGPSTime[c.Last] = gpsTime
	} else {
		// The last integer difference was *not* zero
		currDiff64 := int64(gpsTime) - int64(c.LastGPSTime[c.Last])
		currDiff32 := int32(currDiff64)

		if currDiff64 == int64(currDiff32) {
			// Compute multiplier between current and last integer difference
			// (float32 division and quantization, exactly like the C++)
			multi := i32Quantize(float32(currDiff32) / float32(c.LastGPSTimeDiff[c.Last]))

			// Compress the residual depending on the multiplier
			if multi == 1 {
				// The case we assume most often for regular spaced pulses
				w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, 1)
				if err := c.IcGPSTime.Compress(c.LastGPSTimeDiff[c.Last], currDiff32, 1); err != nil {
					return err
				}
				c.MultiExtremeCounter[c.Last] = 0
			} else if multi > 0 {
				if multi < gpstimeMulti2 {
					// Positive multipliers up to 500 are compressed directly
					w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, uint32(multi))
					gtCtx := uint32(3)
					if multi < 10 {
						gtCtx = 2
					}
					if err := c.IcGPSTime.Compress(multi*c.LastGPSTimeDiff[c.Last], currDiff32, gtCtx); err != nil {
						return err
					}
				} else {
					w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, uint32(gpstimeMulti2))
					if err := c.IcGPSTime.Compress(int32(gpstimeMulti2)*c.LastGPSTimeDiff[c.Last], currDiff32, 4); err != nil {
						return err
					}
					c.MultiExtremeCounter[c.Last]++
					if c.MultiExtremeCounter[c.Last] > 3 {
						c.LastGPSTimeDiff[c.Last] = currDiff32
						c.MultiExtremeCounter[c.Last] = 0
					}
				}
			} else if multi < 0 {
				if multi > gpstimeMultiMinus2 {
					// Negative multipliers larger than -10 are compressed directly
					w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, uint32(int32(gpstimeMulti2)-multi))
					if err := c.IcGPSTime.Compress(multi*c.LastGPSTimeDiff[c.Last], currDiff32, 5); err != nil {
						return err
					}
				} else {
					w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, uint32(gpstimeMulti2-gpstimeMultiMinus2))
					if err := c.IcGPSTime.Compress(int32(gpstimeMultiMinus2)*c.LastGPSTimeDiff[c.Last], currDiff32, 6); err != nil {
						return err
					}
					c.MultiExtremeCounter[c.Last]++
					if c.MultiExtremeCounter[c.Last] > 3 {
						c.LastGPSTimeDiff[c.Last] = currDiff32
						c.MultiExtremeCounter[c.Last] = 0
					}
				}
			} else {
				// multi == 0
				w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, 0)
				if err := c.IcGPSTime.Compress(0, currDiff32, 7); err != nil {
					return err
				}
				c.MultiExtremeCounter[c.Last]++
				if c.MultiExtremeCounter[c.Last] > 3 {
					c.LastGPSTimeDiff[c.Last] = currDiff32
					c.MultiExtremeCounter[c.Last] = 0
				}
			}
		} else {
			// The difference is huge; maybe it belongs to another sequence
			for i := uint32(1); i < 4; i++ {
				otherDiff64 := int64(gpsTime) - int64(c.LastGPSTime[(c.Last+i)&3])
				if otherDiff64 == int64(int32(otherDiff64)) {
					w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, gpstimeMultiCodeFullV3+i)
					c.Last = (c.Last + i) & 3
					return w.writeGPSTime(c, gpsTime)
				}
			}
			// No other sequence found. Start a new sequence.
			w.encGPSTime.EncodeSymbol(c.MGPSTimeMulti, gpstimeMultiCodeFullV3)
			if err := c.IcGPSTime.Compress(int32(c.LastGPSTime[c.Last]>>32), int32(gpsTime>>32), 8); err != nil {
				return err
			}
			w.encGPSTime.WriteInt(uint32(gpsTime))
			c.Next = (c.Next + 1) & 3
			c.Last = c.Next
			c.LastGPSTimeDiff[c.Last] = 0
			c.MultiExtremeCounter[c.Last] = 0
		}
		c.LastGPSTime[c.Last] = gpsTime
	}
	return nil
}

// ChunkSizes finalizes the per-chunk layer encoders and writes the nine layer
// byte counts (u32 LE) to the main stream. Sizes are sampled AFTER Done()
// (the flush tail bytes count); unchanged layers are written as 0.
// C++ original: LASwriteItemCompressed_POINT14_v3::chunk_sizes()
func (w *point14v34Writer) ChunkSizes() error {
	outstream := w.enc.GetByteStreamOut()

	// Finish the encoders: channel_returns_XY and Z unconditionally, the
	// other seven only if their layer changed.
	if _, err := w.encChRXY.Done(); err != nil {
		return err
	}
	if _, err := w.encZ.Done(); err != nil {
		return err
	}
	if w.changedClass {
		if _, err := w.encClass.Done(); err != nil {
			return err
		}
	}
	if w.changedFlags {
		if _, err := w.encFlags.Done(); err != nil {
			return err
		}
	}
	if w.changedIntensity {
		if _, err := w.encIntensity.Done(); err != nil {
			return err
		}
	}
	if w.changedScanAngle {
		if _, err := w.encScanAngle.Done(); err != nil {
			return err
		}
	}
	if w.changedUserData {
		if _, err := w.encUserData.Done(); err != nil {
			return err
		}
	}
	if w.changedPointSource {
		if _, err := w.encPointSource.Done(); err != nil {
			return err
		}
	}
	if w.changedGPSTime {
		if _, err := w.encGPSTime.Done(); err != nil {
			return err
		}
	}

	// Output the sizes of all layers
	buf := make([]byte, 4)
	put := func(n uint32) error {
		binary.LittleEndian.PutUint32(buf, n)
		return outstream.Put32bitsLE(buf)
	}
	condSize := func(changed bool, s *ByteStreamOutArray) uint32 {
		if changed {
			return uint32(s.GetCurr())
		}
		return 0
	}
	if err := put(uint32(w.outChRXY.GetCurr())); err != nil {
		return err
	}
	if err := put(uint32(w.outZ.GetCurr())); err != nil {
		return err
	}
	if err := put(condSize(w.changedClass, w.outClass)); err != nil {
		return err
	}
	if err := put(condSize(w.changedFlags, w.outFlags)); err != nil {
		return err
	}
	if err := put(condSize(w.changedIntensity, w.outIntensity)); err != nil {
		return err
	}
	if err := put(condSize(w.changedScanAngle, w.outScanAngle)); err != nil {
		return err
	}
	if err := put(condSize(w.changedUserData, w.outUserData)); err != nil {
		return err
	}
	if err := put(condSize(w.changedPointSource, w.outPointSource)); err != nil {
		return err
	}
	if err := put(condSize(w.changedGPSTime, w.outGPSTime)); err != nil {
		return err
	}
	return nil
}

// ChunkBytes writes the finished layer payloads to the main stream in the
// same order as ChunkSizes; unchanged layers contribute nothing.
// C++ original: LASwriteItemCompressed_POINT14_v3::chunk_bytes()
func (w *point14v34Writer) ChunkBytes() error {
	outstream := w.enc.GetByteStreamOut()

	put := func(s *ByteStreamOutArray) error {
		return outstream.PutBytes(s.GetData()[:s.GetCurr()])
	}
	if err := put(w.outChRXY); err != nil {
		return err
	}
	if err := put(w.outZ); err != nil {
		return err
	}
	if w.changedClass {
		if err := put(w.outClass); err != nil {
			return err
		}
	}
	if w.changedFlags {
		if err := put(w.outFlags); err != nil {
			return err
		}
	}
	if w.changedIntensity {
		if err := put(w.outIntensity); err != nil {
			return err
		}
	}
	if w.changedScanAngle {
		if err := put(w.outScanAngle); err != nil {
			return err
		}
	}
	if w.changedUserData {
		if err := put(w.outUserData); err != nil {
			return err
		}
	}
	if w.changedPointSource {
		if err := put(w.outPointSource); err != nil {
			return err
		}
	}
	if w.changedGPSTime {
		if err := put(w.outGPSTime); err != nil {
			return err
		}
	}
	return nil
}

// LASwriteItemCompressedPoint14v3 is the v3 layered POINT14 writer.
type LASwriteItemCompressedPoint14v3 struct{ *point14v34Writer }

// NewLASwriteItemCompressedPoint14v3 creates a v3 layered POINT14 writer.
// enc is the dummy encoder that provides access to the main output stream.
func NewLASwriteItemCompressedPoint14v3(enc *ArithmeticEncoder) *LASwriteItemCompressedPoint14v3 {
	return &LASwriteItemCompressedPoint14v3{newPoint14v34Writer(enc, false)}
}

// ---------------------------------------------------------------------------
// RGB14 v3/v4 — layered compression for LAS 1.4 RGB (3×uint16), 1 layer
// ---------------------------------------------------------------------------

type rgb14v34Writer struct {
	enc *ArithmeticEncoder // only used for main outstream access
	v4  bool

	outRGB *ByteStreamOutArray
	encRGB *ArithmeticEncoder

	changedRGB bool

	currentContext uint32
	contexts       [4]LAScontextRGB14
}

func newRGB14v34Writer(enc *ArithmeticEncoder, v4 bool) *rgb14v34Writer {
	return &rgb14v34Writer{enc: enc, v4: v4}
}

// Init seeds the per-chunk state from the chunk's first (raw) point.
// C++ original: LASwriteItemCompressed_RGB14_v3::init(const U8* item, U32& context)
func (w *rgb14v34Writer) Init(item []byte, ctx *uint32) error {
	if w.outRGB == nil {
		w.outRGB = NewByteStreamOutArray()
		w.encRGB = NewArithmeticEncoder()
	} else {
		w.outRGB.Reset()
	}
	if err := w.encRGB.Init(w.outRGB); err != nil {
		return err
	}

	w.changedRGB = false

	for c := range 4 {
		w.contexts[c].Unused = true
	}

	// All other items use the context set by the POINT14 writer
	w.currentContext = *ctx

	w.createAndInitModelsAndCompressors(w.currentContext, item)
	return nil
}

func (w *rgb14v34Writer) createAndInitModelsAndCompressors(ctx uint32, item []byte) {
	c := &w.contexts[ctx]

	if c.MByteUsed == nil {
		c.MByteUsed = w.encRGB.CreateSymbolModel(128)
		c.MRGBDiff0 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff1 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff2 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff3 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff4 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff5 = w.encRGB.CreateSymbolModel(256)
	}

	w.encRGB.InitSymbolModel(c.MByteUsed, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff0, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff1, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff2, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff3, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff4, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff5, nil)

	c.LastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	c.LastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	c.LastItem[2] = binary.LittleEndian.Uint16(item[4:6])

	c.Unused = false
}

// Write compresses one RGB point (6 bytes = 3×uint16 LE).
// C++ original: LASwriteItemCompressed_RGB14_v3::write(const U8* item, U32& context)
func (w *rgb14v34Writer) Write(item []byte, context *uint32) error {
	c := &w.contexts[w.currentContext]
	lastItem := c.LastItem[:]

	// Check for context switch
	if w.currentContext != *context {
		w.currentContext = *context
		if w.contexts[w.currentContext].Unused {
			var seed [6]byte
			binary.LittleEndian.PutUint16(seed[0:2], lastItem[0])
			binary.LittleEndian.PutUint16(seed[2:4], lastItem[1])
			binary.LittleEndian.PutUint16(seed[4:6], lastItem[2])
			w.createAndInitModelsAndCompressors(w.currentContext, seed[:])
			lastItem = w.contexts[w.currentContext].LastItem[:]
		} else if w.v4 {
			// v4 rebinds unconditionally; v3 keeps the stale last_item of the
			// previous context when the new one was already used.
			lastItem = w.contexts[w.currentContext].LastItem[:]
		}
		c = &w.contexts[w.currentContext] // always use new context's models
	}

	itemR := binary.LittleEndian.Uint16(item[0:2])
	itemG := binary.LittleEndian.Uint16(item[2:4])
	itemB := binary.LittleEndian.Uint16(item[4:6])

	diffL := 0
	diffH := 0
	sym := boolU32(lastItem[0]&0x00FF != itemR&0x00FF)
	sym |= boolU32(lastItem[0]&0xFF00 != itemR&0xFF00) << 1
	sym |= boolU32(lastItem[1]&0x00FF != itemG&0x00FF) << 2
	sym |= boolU32(lastItem[1]&0xFF00 != itemG&0xFF00) << 3
	sym |= boolU32(lastItem[2]&0x00FF != itemB&0x00FF) << 4
	sym |= boolU32(lastItem[2]&0xFF00 != itemB&0xFF00) << 5
	sym |= boolU32(itemR&0x00FF != itemG&0x00FF || itemR&0x00FF != itemB&0x00FF ||
		itemR&0xFF00 != itemG&0xFF00 || itemR&0xFF00 != itemB&0xFF00) << 6
	w.encRGB.EncodeSymbol(c.MByteUsed, sym)

	if sym&(1<<0) != 0 {
		diffL = int(itemR&255) - int(lastItem[0]&255)
		w.encRGB.EncodeSymbol(c.MRGBDiff0, uint32(u8Fold(diffL)))
	}
	if sym&(1<<1) != 0 {
		diffH = int(itemR>>8) - int(lastItem[0]>>8)
		w.encRGB.EncodeSymbol(c.MRGBDiff1, uint32(u8Fold(diffH)))
	}
	if sym&(1<<6) != 0 {
		if sym&(1<<2) != 0 {
			corr := int(itemG&255) - int(u8Clamp(int32(diffL+int(lastItem[1]&255))))
			w.encRGB.EncodeSymbol(c.MRGBDiff2, uint32(u8Fold(corr)))
		}
		if sym&(1<<4) != 0 {
			diffL = (diffL + int(itemG&255) - int(lastItem[1]&255)) / 2
			corr := int(itemB&255) - int(u8Clamp(int32(diffL+int(lastItem[2]&255))))
			w.encRGB.EncodeSymbol(c.MRGBDiff4, uint32(u8Fold(corr)))
		}
		if sym&(1<<3) != 0 {
			corr := int(itemG>>8) - int(u8Clamp(int32(diffH+int(lastItem[1]>>8))))
			w.encRGB.EncodeSymbol(c.MRGBDiff3, uint32(u8Fold(corr)))
		}
		if sym&(1<<5) != 0 {
			diffH = (diffH + int(itemG>>8) - int(lastItem[1]>>8)) / 2
			corr := int(itemB>>8) - int(u8Clamp(int32(diffH+int(lastItem[2]>>8))))
			w.encRGB.EncodeSymbol(c.MRGBDiff5, uint32(u8Fold(corr)))
		}
	}
	if sym != 0 {
		w.changedRGB = true
	}
	lastItem[0], lastItem[1], lastItem[2] = itemR, itemG, itemB
	return nil
}

// ChunkSizes finalizes the RGB layer encoder (unconditionally, like the C++)
// and writes the layer byte count (0 when unchanged) to the main stream.
func (w *rgb14v34Writer) ChunkSizes() error {
	outstream := w.enc.GetByteStreamOut()

	if _, err := w.encRGB.Done(); err != nil {
		return err
	}

	numBytes := uint32(0)
	if w.changedRGB {
		numBytes = uint32(w.outRGB.GetCurr())
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, numBytes)
	return outstream.Put32bitsLE(buf)
}

// ChunkBytes writes the RGB layer payload to the main stream if it changed.
func (w *rgb14v34Writer) ChunkBytes() error {
	outstream := w.enc.GetByteStreamOut()

	if w.changedRGB {
		return outstream.PutBytes(w.outRGB.GetData()[:w.outRGB.GetCurr()])
	}
	return nil
}

// LASwriteItemCompressedRGB14v3 is the v3 layered RGB14 writer.
type LASwriteItemCompressedRGB14v3 struct{ *rgb14v34Writer }

// NewLASwriteItemCompressedRGB14v3 creates a v3 layered RGB14 writer.
func NewLASwriteItemCompressedRGB14v3(enc *ArithmeticEncoder) *LASwriteItemCompressedRGB14v3 {
	return &LASwriteItemCompressedRGB14v3{newRGB14v34Writer(enc, false)}
}

// ---------------------------------------------------------------------------
// RGBNIR14 v3/v4 — layered compression for LAS 1.4 RGB+NIR (4×uint16),
// 2 layers (RGB, NIR)
// ---------------------------------------------------------------------------

type rgbnir14v34Writer struct {
	enc *ArithmeticEncoder // only used for main outstream access
	v4  bool

	outRGB *ByteStreamOutArray
	outNIR *ByteStreamOutArray
	encRGB *ArithmeticEncoder
	encNIR *ArithmeticEncoder

	changedRGB bool
	changedNIR bool

	currentContext uint32
	contexts       [4]LAScontextRGBNIR14
}

func newRGBNIR14v34Writer(enc *ArithmeticEncoder, v4 bool) *rgbnir14v34Writer {
	return &rgbnir14v34Writer{enc: enc, v4: v4}
}

// Init seeds the per-chunk state from the chunk's first (raw) point.
// C++ original: LASwriteItemCompressed_RGBNIR14_v3::init(const U8* item, U32& context)
func (w *rgbnir14v34Writer) Init(item []byte, ctx *uint32) error {
	if w.outRGB == nil {
		w.outRGB = NewByteStreamOutArray()
		w.outNIR = NewByteStreamOutArray()
		w.encRGB = NewArithmeticEncoder()
		w.encNIR = NewArithmeticEncoder()
	} else {
		w.outRGB.Reset()
		w.outNIR.Reset()
	}
	if err := w.encRGB.Init(w.outRGB); err != nil {
		return err
	}
	if err := w.encNIR.Init(w.outNIR); err != nil {
		return err
	}

	w.changedRGB = false
	w.changedNIR = false

	for c := range 4 {
		w.contexts[c].Unused = true
	}

	// All other items use the context set by the POINT14 writer
	w.currentContext = *ctx

	w.createAndInitModelsAndCompressors(w.currentContext, item)
	return nil
}

func (w *rgbnir14v34Writer) createAndInitModelsAndCompressors(ctx uint32, item []byte) {
	c := &w.contexts[ctx]

	if c.MRGBBytesUsed == nil {
		c.MRGBBytesUsed = w.encRGB.CreateSymbolModel(128)
		c.MRGBDiff0 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff1 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff2 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff3 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff4 = w.encRGB.CreateSymbolModel(256)
		c.MRGBDiff5 = w.encRGB.CreateSymbolModel(256)

		c.MNIRBytesUsed = w.encNIR.CreateSymbolModel(4)
		c.MNIRDiff0 = w.encNIR.CreateSymbolModel(256)
		c.MNIRDiff1 = w.encNIR.CreateSymbolModel(256)
	}

	w.encRGB.InitSymbolModel(c.MRGBBytesUsed, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff0, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff1, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff2, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff3, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff4, nil)
	w.encRGB.InitSymbolModel(c.MRGBDiff5, nil)

	w.encNIR.InitSymbolModel(c.MNIRBytesUsed, nil)
	w.encNIR.InitSymbolModel(c.MNIRDiff0, nil)
	w.encNIR.InitSymbolModel(c.MNIRDiff1, nil)

	c.LastItem[0] = binary.LittleEndian.Uint16(item[0:2])
	c.LastItem[1] = binary.LittleEndian.Uint16(item[2:4])
	c.LastItem[2] = binary.LittleEndian.Uint16(item[4:6])
	c.LastItem[3] = binary.LittleEndian.Uint16(item[6:8])

	c.Unused = false
}

// Write compresses one RGBNIR point (8 bytes = 4×uint16 LE).
// C++ original: LASwriteItemCompressed_RGBNIR14_v3::write(const U8* item, U32& context)
func (w *rgbnir14v34Writer) Write(item []byte, context *uint32) error {
	c := &w.contexts[w.currentContext]
	lastItem := c.LastItem[:]

	// Check for context switch
	if w.currentContext != *context {
		w.currentContext = *context
		if w.contexts[w.currentContext].Unused {
			var seed [8]byte
			binary.LittleEndian.PutUint16(seed[0:2], lastItem[0])
			binary.LittleEndian.PutUint16(seed[2:4], lastItem[1])
			binary.LittleEndian.PutUint16(seed[4:6], lastItem[2])
			binary.LittleEndian.PutUint16(seed[6:8], lastItem[3])
			w.createAndInitModelsAndCompressors(w.currentContext, seed[:])
			lastItem = w.contexts[w.currentContext].LastItem[:]
		} else if w.v4 {
			lastItem = w.contexts[w.currentContext].LastItem[:]
		}
		c = &w.contexts[w.currentContext] // always use new context's models
	}

	itemR := binary.LittleEndian.Uint16(item[0:2])
	itemG := binary.LittleEndian.Uint16(item[2:4])
	itemB := binary.LittleEndian.Uint16(item[4:6])
	itemN := binary.LittleEndian.Uint16(item[6:8])

	// ---- compress RGB layer (identical to RGB14) ----

	diffL := 0
	diffH := 0
	sym := boolU32(lastItem[0]&0x00FF != itemR&0x00FF)
	sym |= boolU32(lastItem[0]&0xFF00 != itemR&0xFF00) << 1
	sym |= boolU32(lastItem[1]&0x00FF != itemG&0x00FF) << 2
	sym |= boolU32(lastItem[1]&0xFF00 != itemG&0xFF00) << 3
	sym |= boolU32(lastItem[2]&0x00FF != itemB&0x00FF) << 4
	sym |= boolU32(lastItem[2]&0xFF00 != itemB&0xFF00) << 5
	sym |= boolU32(itemR&0x00FF != itemG&0x00FF || itemR&0x00FF != itemB&0x00FF ||
		itemR&0xFF00 != itemG&0xFF00 || itemR&0xFF00 != itemB&0xFF00) << 6
	w.encRGB.EncodeSymbol(c.MRGBBytesUsed, sym)

	if sym&(1<<0) != 0 {
		diffL = int(itemR&255) - int(lastItem[0]&255)
		w.encRGB.EncodeSymbol(c.MRGBDiff0, uint32(u8Fold(diffL)))
	}
	if sym&(1<<1) != 0 {
		diffH = int(itemR>>8) - int(lastItem[0]>>8)
		w.encRGB.EncodeSymbol(c.MRGBDiff1, uint32(u8Fold(diffH)))
	}
	if sym&(1<<6) != 0 {
		if sym&(1<<2) != 0 {
			corr := int(itemG&255) - int(u8Clamp(int32(diffL+int(lastItem[1]&255))))
			w.encRGB.EncodeSymbol(c.MRGBDiff2, uint32(u8Fold(corr)))
		}
		if sym&(1<<4) != 0 {
			diffL = (diffL + int(itemG&255) - int(lastItem[1]&255)) / 2
			corr := int(itemB&255) - int(u8Clamp(int32(diffL+int(lastItem[2]&255))))
			w.encRGB.EncodeSymbol(c.MRGBDiff4, uint32(u8Fold(corr)))
		}
		if sym&(1<<3) != 0 {
			corr := int(itemG>>8) - int(u8Clamp(int32(diffH+int(lastItem[1]>>8))))
			w.encRGB.EncodeSymbol(c.MRGBDiff3, uint32(u8Fold(corr)))
		}
		if sym&(1<<5) != 0 {
			diffH = (diffH + int(itemG>>8) - int(lastItem[1]>>8)) / 2
			corr := int(itemB>>8) - int(u8Clamp(int32(diffH+int(lastItem[2]>>8))))
			w.encRGB.EncodeSymbol(c.MRGBDiff5, uint32(u8Fold(corr)))
		}
	}
	if sym != 0 {
		w.changedRGB = true
	}

	// ---- compress NIR layer ----

	sym = boolU32(lastItem[3]&0x00FF != itemN&0x00FF)
	sym |= boolU32(lastItem[3]&0xFF00 != itemN&0xFF00) << 1
	w.encNIR.EncodeSymbol(c.MNIRBytesUsed, sym)
	if sym&(1<<0) != 0 {
		diffL = int(itemN&255) - int(lastItem[3]&255)
		w.encNIR.EncodeSymbol(c.MNIRDiff0, uint32(u8Fold(diffL)))
	}
	if sym&(1<<1) != 0 {
		diffH = int(itemN>>8) - int(lastItem[3]>>8)
		w.encNIR.EncodeSymbol(c.MNIRDiff1, uint32(u8Fold(diffH)))
	}
	if sym != 0 {
		w.changedNIR = true
	}

	lastItem[0], lastItem[1], lastItem[2], lastItem[3] = itemR, itemG, itemB, itemN
	return nil
}

// ChunkSizes finalizes both layer encoders (unconditionally, like the C++)
// and writes the RGB then NIR layer byte counts (0 when unchanged).
func (w *rgbnir14v34Writer) ChunkSizes() error {
	outstream := w.enc.GetByteStreamOut()

	if _, err := w.encRGB.Done(); err != nil {
		return err
	}
	if _, err := w.encNIR.Done(); err != nil {
		return err
	}

	buf := make([]byte, 4)
	numBytes := uint32(0)
	if w.changedRGB {
		numBytes = uint32(w.outRGB.GetCurr())
	}
	binary.LittleEndian.PutUint32(buf, numBytes)
	if err := outstream.Put32bitsLE(buf); err != nil {
		return err
	}

	numBytes = 0
	if w.changedNIR {
		numBytes = uint32(w.outNIR.GetCurr())
	}
	binary.LittleEndian.PutUint32(buf, numBytes)
	return outstream.Put32bitsLE(buf)
}

// ChunkBytes writes the RGB then NIR layer payloads if they changed.
func (w *rgbnir14v34Writer) ChunkBytes() error {
	outstream := w.enc.GetByteStreamOut()

	if w.changedRGB {
		if err := outstream.PutBytes(w.outRGB.GetData()[:w.outRGB.GetCurr()]); err != nil {
			return err
		}
	}
	if w.changedNIR {
		if err := outstream.PutBytes(w.outNIR.GetData()[:w.outNIR.GetCurr()]); err != nil {
			return err
		}
	}
	return nil
}

// LASwriteItemCompressedRGBNIR14v3 is the v3 layered RGBNIR14 writer.
type LASwriteItemCompressedRGBNIR14v3 struct{ *rgbnir14v34Writer }

// NewLASwriteItemCompressedRGBNIR14v3 creates a v3 layered RGBNIR14 writer.
func NewLASwriteItemCompressedRGBNIR14v3(enc *ArithmeticEncoder) *LASwriteItemCompressedRGBNIR14v3 {
	return &LASwriteItemCompressedRGBNIR14v3{newRGBNIR14v34Writer(enc, false)}
}

// ---------------------------------------------------------------------------
// WAVEPACKET14 v3/v4 — layered compression for LAS 1.4 wave packet (29 bytes),
// 1 layer
// ---------------------------------------------------------------------------

type wavepacket14v34Writer struct {
	enc *ArithmeticEncoder // only used for main outstream access
	v4  bool

	outWavepacket *ByteStreamOutArray
	encWavepacket *ArithmeticEncoder

	changedWavepacket bool

	currentContext uint32
	contexts       [4]LAScontextWAVEPACKET14
}

func newWavepacket14v34Writer(enc *ArithmeticEncoder, v4 bool) *wavepacket14v34Writer {
	return &wavepacket14v34Writer{enc: enc, v4: v4}
}

// Init seeds the per-chunk state from the chunk's first (raw) point.
// C++ original: LASwriteItemCompressed_WAVEPACKET14_v3::init(const U8* item, U32& context)
func (w *wavepacket14v34Writer) Init(item []byte, ctx *uint32) error {
	if w.outWavepacket == nil {
		w.outWavepacket = NewByteStreamOutArray()
		w.encWavepacket = NewArithmeticEncoder()
	} else {
		w.outWavepacket.Reset()
	}
	if err := w.encWavepacket.Init(w.outWavepacket); err != nil {
		return err
	}

	w.changedWavepacket = false

	for c := range 4 {
		w.contexts[c].Unused = true
	}

	// All other items use the context set by the POINT14 writer
	w.currentContext = *ctx

	w.createAndInitModelsAndCompressors(w.currentContext, item)
	return nil
}

func (w *wavepacket14v34Writer) createAndInitModelsAndCompressors(ctx uint32, item []byte) {
	c := &w.contexts[ctx]

	if c.MPacketIndex == nil {
		c.MPacketIndex = w.encWavepacket.CreateSymbolModel(256)
		c.MOffsetDiff[0] = w.encWavepacket.CreateSymbolModel(4)
		c.MOffsetDiff[1] = w.encWavepacket.CreateSymbolModel(4)
		c.MOffsetDiff[2] = w.encWavepacket.CreateSymbolModel(4)
		c.MOffsetDiff[3] = w.encWavepacket.CreateSymbolModel(4)
		c.IcOffsetDiff = NewIntegerCompressor(w.encWavepacket, 32, 1, 8, 0)
		c.IcPacketSize = NewIntegerCompressor(w.encWavepacket, 32, 1, 8, 0)
		c.IcReturnPoint = NewIntegerCompressor(w.encWavepacket, 32, 1, 8, 0)
		c.IcXYZ = NewIntegerCompressor(w.encWavepacket, 32, 3, 8, 0)
	}

	w.encWavepacket.InitSymbolModel(c.MPacketIndex, nil)
	w.encWavepacket.InitSymbolModel(c.MOffsetDiff[0], nil)
	w.encWavepacket.InitSymbolModel(c.MOffsetDiff[1], nil)
	w.encWavepacket.InitSymbolModel(c.MOffsetDiff[2], nil)
	w.encWavepacket.InitSymbolModel(c.MOffsetDiff[3], nil)
	c.IcOffsetDiff.InitCompressor()
	c.IcPacketSize.InitCompressor()
	c.IcReturnPoint.InitCompressor()
	c.IcXYZ.InitCompressor()

	c.LastDiff32 = 0
	c.SymLastOffsetDiff = 0
	copy(c.LastItem[:29], item[:29])

	c.Unused = false
}

// Write compresses one WAVEPACKET point (29 bytes).
// C++ original: LASwriteItemCompressed_WAVEPACKET14_v3::write(const U8* item, U32& context)
func (w *wavepacket14v34Writer) Write(item []byte, context *uint32) error {
	c := &w.contexts[w.currentContext]
	lastItem := c.LastItem[:]

	// Check for context switch
	if w.currentContext != *context {
		w.currentContext = *context
		if w.contexts[w.currentContext].Unused {
			w.createAndInitModelsAndCompressors(w.currentContext, lastItem[:29])
			lastItem = w.contexts[w.currentContext].LastItem[:]
		} else if w.v4 {
			lastItem = w.contexts[w.currentContext].LastItem[:]
		}
		c = &w.contexts[w.currentContext] // always use new context's models
	}

	if !bytes.Equal(item[:29], lastItem[:29]) {
		w.changedWavepacket = true
	}

	// Byte 0: packet index
	w.encWavepacket.EncodeSymbol(c.MPacketIndex, uint32(item[0]))

	// Bytes 1-28: wave packet body
	thisM := UnpackLASwavepacket13(item[1:])
	lastM := UnpackLASwavepacket13(lastItem[1:])

	// Calculate the difference between the two offsets
	currDiff64 := int64(thisM.Offset) - int64(lastM.Offset)
	currDiff32 := int32(currDiff64)

	if currDiff64 == int64(currDiff32) {
		// The current difference can be represented with 32 bits
		switch {
		case currDiff32 == 0:
			w.encWavepacket.EncodeSymbol(c.MOffsetDiff[c.SymLastOffsetDiff], 0)
			c.SymLastOffsetDiff = 0
		case currDiff32 == int32(lastM.PacketSize):
			w.encWavepacket.EncodeSymbol(c.MOffsetDiff[c.SymLastOffsetDiff], 1)
			c.SymLastOffsetDiff = 1
		default:
			w.encWavepacket.EncodeSymbol(c.MOffsetDiff[c.SymLastOffsetDiff], 2)
			c.SymLastOffsetDiff = 2
			if err := c.IcOffsetDiff.Compress(c.LastDiff32, currDiff32, 0); err != nil {
				return err
			}
			c.LastDiff32 = currDiff32
		}
	} else {
		w.encWavepacket.EncodeSymbol(c.MOffsetDiff[c.SymLastOffsetDiff], 3)
		c.SymLastOffsetDiff = 3
		w.encWavepacket.WriteInt64(thisM.Offset)
	}

	if err := c.IcPacketSize.Compress(int32(lastM.PacketSize), int32(thisM.PacketSize), 0); err != nil {
		return err
	}
	if err := c.IcReturnPoint.Compress(int32(math.Float32bits(lastM.ReturnPoint)), int32(math.Float32bits(thisM.ReturnPoint)), 0); err != nil {
		return err
	}
	if err := c.IcXYZ.Compress(int32(math.Float32bits(lastM.X)), int32(math.Float32bits(thisM.X)), 0); err != nil {
		return err
	}
	if err := c.IcXYZ.Compress(int32(math.Float32bits(lastM.Y)), int32(math.Float32bits(thisM.Y)), 1); err != nil {
		return err
	}
	if err := c.IcXYZ.Compress(int32(math.Float32bits(lastM.Z)), int32(math.Float32bits(thisM.Z)), 2); err != nil {
		return err
	}

	copy(lastItem[:29], item[:29])
	return nil
}

// ChunkSizes finalizes the wavepacket layer encoder (unconditionally) and
// writes the layer byte count (0 when unchanged) to the main stream.
func (w *wavepacket14v34Writer) ChunkSizes() error {
	outstream := w.enc.GetByteStreamOut()

	if _, err := w.encWavepacket.Done(); err != nil {
		return err
	}

	numBytes := uint32(0)
	if w.changedWavepacket {
		numBytes = uint32(w.outWavepacket.GetCurr())
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, numBytes)
	return outstream.Put32bitsLE(buf)
}

// ChunkBytes writes the wavepacket layer payload if it changed.
func (w *wavepacket14v34Writer) ChunkBytes() error {
	outstream := w.enc.GetByteStreamOut()

	if w.changedWavepacket {
		return outstream.PutBytes(w.outWavepacket.GetData()[:w.outWavepacket.GetCurr()])
	}
	return nil
}

// LASwriteItemCompressedWavepacket14v3 is the v3 layered WAVEPACKET14 writer.
type LASwriteItemCompressedWavepacket14v3 struct{ *wavepacket14v34Writer }

// NewLASwriteItemCompressedWavepacket14v3 creates a v3 layered WAVEPACKET14 writer.
func NewLASwriteItemCompressedWavepacket14v3(enc *ArithmeticEncoder) *LASwriteItemCompressedWavepacket14v3 {
	return &LASwriteItemCompressedWavepacket14v3{newWavepacket14v34Writer(enc, false)}
}

// ---------------------------------------------------------------------------
// BYTE14 v3/v4 — layered compression for LAS 1.4 extra bytes, one layer
// (outstream + encoder) per byte
// ---------------------------------------------------------------------------

type byte14v34Writer struct {
	enc *ArithmeticEncoder // only used for main outstream access
	v4  bool

	number uint32

	outBytes []*ByteStreamOutArray
	encBytes []*ArithmeticEncoder

	changedBytes []bool

	currentContext uint32
	contexts       [4]LAScontextBYTE14
}

func newByte14v34Writer(enc *ArithmeticEncoder, number uint32, v4 bool) *byte14v34Writer {
	return &byte14v34Writer{
		enc:          enc,
		v4:           v4,
		number:       number,
		changedBytes: make([]bool, number),
	}
}

// Init seeds the per-chunk state from the chunk's first (raw) point.
// C++ original: LASwriteItemCompressed_BYTE14_v3::init(const U8* item, U32& context)
func (w *byte14v34Writer) Init(item []byte, ctx *uint32) error {
	if w.outBytes == nil {
		w.outBytes = make([]*ByteStreamOutArray, w.number)
		w.encBytes = make([]*ArithmeticEncoder, w.number)
		for i := uint32(0); i < w.number; i++ {
			w.outBytes[i] = NewByteStreamOutArray()
			w.encBytes[i] = NewArithmeticEncoder()
		}
	} else {
		for i := uint32(0); i < w.number; i++ {
			w.outBytes[i].Reset()
		}
	}
	for i := uint32(0); i < w.number; i++ {
		if err := w.encBytes[i].Init(w.outBytes[i]); err != nil {
			return err
		}
	}

	for i := uint32(0); i < w.number; i++ {
		w.changedBytes[i] = false
	}

	for c := range 4 {
		w.contexts[c].Unused = true
	}

	// All other items use the context set by the POINT14 writer
	w.currentContext = *ctx

	w.createAndInitModelsAndCompressors(w.currentContext, item)
	return nil
}

func (w *byte14v34Writer) createAndInitModelsAndCompressors(ctx uint32, item []byte) {
	c := &w.contexts[ctx]

	if c.MBytes == nil {
		c.MBytes = make([]*ArithmeticModel, w.number)
		for i := uint32(0); i < w.number; i++ {
			c.MBytes[i] = w.encBytes[i].CreateSymbolModel(256)
		}
		c.LastItem = make([]uint8, w.number)
	}

	for i := uint32(0); i < w.number; i++ {
		w.encBytes[i].InitSymbolModel(c.MBytes[i], nil)
	}

	copy(c.LastItem, item[:w.number])

	c.Unused = false
}

// Write compresses one BYTE14 point (number bytes, one layer per byte).
// C++ original: LASwriteItemCompressed_BYTE14_v3::write(const U8* item, U32& context)
func (w *byte14v34Writer) Write(item []byte, context *uint32) error {
	c := &w.contexts[w.currentContext]
	lastItem := c.LastItem

	// Check for context switch
	if w.currentContext != *context {
		w.currentContext = *context
		if w.contexts[w.currentContext].Unused {
			w.createAndInitModelsAndCompressors(w.currentContext, lastItem)
			lastItem = w.contexts[w.currentContext].LastItem
		} else if w.v4 {
			lastItem = w.contexts[w.currentContext].LastItem
		}
		c = &w.contexts[w.currentContext] // always use new context's models
	}

	for i := uint32(0); i < w.number; i++ {
		diff := int(item[i]) - int(lastItem[i])
		w.encBytes[i].EncodeSymbol(c.MBytes[i], uint32(u8Fold(diff)))
		if diff != 0 {
			w.changedBytes[i] = true
			lastItem[i] = item[i]
		}
	}
	return nil
}

// ChunkSizes finalizes every per-byte encoder (unconditionally) and writes
// each layer's byte count (0 when unchanged) to the main stream.
func (w *byte14v34Writer) ChunkSizes() error {
	outstream := w.enc.GetByteStreamOut()

	buf := make([]byte, 4)
	for i := uint32(0); i < w.number; i++ {
		if _, err := w.encBytes[i].Done(); err != nil {
			return err
		}
		numBytes := uint32(0)
		if w.changedBytes[i] {
			numBytes = uint32(w.outBytes[i].GetCurr())
		}
		binary.LittleEndian.PutUint32(buf, numBytes)
		if err := outstream.Put32bitsLE(buf); err != nil {
			return err
		}
	}
	return nil
}

// ChunkBytes writes each changed per-byte layer payload in order.
func (w *byte14v34Writer) ChunkBytes() error {
	outstream := w.enc.GetByteStreamOut()

	for i := uint32(0); i < w.number; i++ {
		if w.changedBytes[i] {
			if err := outstream.PutBytes(w.outBytes[i].GetData()[:w.outBytes[i].GetCurr()]); err != nil {
				return err
			}
		}
	}
	return nil
}

// LASwriteItemCompressedByte14v3 is the v3 layered BYTE14 writer.
type LASwriteItemCompressedByte14v3 struct{ *byte14v34Writer }

// NewLASwriteItemCompressedByte14v3 creates a v3 layered BYTE14 writer for
// `number` extra bytes.
func NewLASwriteItemCompressedByte14v3(enc *ArithmeticEncoder, number uint32) *LASwriteItemCompressedByte14v3 {
	return &LASwriteItemCompressedByte14v3{newByte14v34Writer(enc, number, false)}
}

// Interface conformance checks.
var (
	_ LASwriteItemCompressed = (*LASwriteItemCompressedPoint14v3)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedRGB14v3)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedRGBNIR14v3)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedWavepacket14v3)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedByte14v3)(nil)
)
