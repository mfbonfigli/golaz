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
// readitem_raw.go — raw (uncompressed) point item readers, ported from
// src/lasreaditemraw.hpp. Implements only LE readers (the dominant LAS encoding).
package laz

import (
	"encoding/binary"
	"math"
)

// ---------------------------------------------------------------------------
// POINT10 — 20-byte raw reader (LAS point types 0-5)
// ---------------------------------------------------------------------------

type LASreadItemRawPoint10LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawPoint10LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:20])
}

// ---------------------------------------------------------------------------
// GPSTIME11 — 8-byte raw reader
// ---------------------------------------------------------------------------

type LASreadItemRawGpsTime11LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawGpsTime11LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:8])
}

// ---------------------------------------------------------------------------
// RGB12 — 6-byte raw reader (3×uint16)
// ---------------------------------------------------------------------------

type LASreadItemRawRGB12LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawRGB12LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:6])
}

// ---------------------------------------------------------------------------
// WAVEPACKET13 — 29-byte raw reader
// ---------------------------------------------------------------------------

type LASreadItemRawWavepacket13LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawWavepacket13LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:29])
}

// ---------------------------------------------------------------------------
// BYTE — variable-size raw reader (extra bytes for types 0-5)
// ---------------------------------------------------------------------------

type LASreadItemRawByte struct {
	LASreadItemRaw
	number uint32
}

func NewLASreadItemRawByte(number uint32) *LASreadItemRawByte {
	return &LASreadItemRawByte{number: number}
}

func (r *LASreadItemRawByte) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:r.number])
}

// ---------------------------------------------------------------------------
// POINT14 — 30-byte on-disk → 40-byte LAStempReadPoint10 layout
//
// The C++ lasreaditemraw.hpp casts the output buffer to LAStempReadPoint10*
// which is a 40-byte struct with alignment padding. The compressed readers
// (LASreadItemCompressed_POINT14_v3/v4) also cast the item buffer to
// LASpoint14* which includes fields up to gps_time at offset 32.
//
// LAStempReadPoint10 layout (C++ struct, LE):
//   [ 0.. 3] X (I32)
//   [ 4.. 7] Y (I32)
//   [ 8..11] Z (I32)
//   [12..13] intensity (U16)
//   [14]     bitfield: return_number:3 | number_of_returns:3 | scan_direction_flag:1 | eofl:1
//   [15]     classification (merged: flags<<5 | class if class<32)
//   [16]     scan_angle_rank (I8) — quantized from scan_angle*0.006
//   [17]     user_data (U8)
//   [18..19] point_source_ID (U16)
//   [20..21] extended_scan_angle (I16) — raw scan_angle from LAS 1.4
//   [22]     bitfield: extended_point_type:2 | extended_scanner_channel:2 | extended_classification_flags:4
//   [23]     extended_classification (U8)
//   [24]     bitfield: extended_return_number:4 | extended_number_of_returns:4
//   [25..27] dummy[3] (alignment padding)
//   [28..31] deleted_flag (U32)
//   [32..39] gps_time (F64)
// ---------------------------------------------------------------------------

type LASreadItemRawPoint14LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawPoint14LE) Read(item []byte, _ *uint32) error {
	if len(item) < 40 {
		return errBufTooSmall
	}
	buf := make([]byte, 30)
	if err := r.instream.GetBytes(buf); err != nil {
		return err
	}

	// Parse 30-byte on-disk LAS 1.4 point payload
	//  [ 0.. 3] X (I32)
	//  [ 4.. 7] Y (I32)
	//  [ 8..11] Z (I32)
	//  [12..13] intensity (U16)
	//  [14]     return_number:4 | number_of_returns:4
	//  [15]     classification_flags:4 | scanner_channel:2 | scan_direction_flag:1 | edge_of_flight_line:1
	//  [16]     classification (U8)
	//  [17]     user_data (U8)
	//  [18..19] scan_angle (I16)
	//  [20..21] point_source_ID (U16)
	//  [22..29] GPS time (F64)

	x := int32(binary.LittleEndian.Uint32(buf[0:4]))
	y := int32(binary.LittleEndian.Uint32(buf[4:8]))
	z := int32(binary.LittleEndian.Uint32(buf[8:12]))
	intensity := binary.LittleEndian.Uint16(buf[12:14])
	returnNum := buf[14] & 0x0F
	numReturns := (buf[14] >> 4) & 0x0F
	classFlags := buf[15] & 0x0F
	scannerCh := (buf[15] >> 4) & 0x03
	scanDir := (buf[15] >> 6) & 0x01
	eofl := (buf[15] >> 7) & 0x01
	classification := buf[16]
	userData := buf[17]
	scanAngle := int16(binary.LittleEndian.Uint16(buf[18:20]))
	pointSourceID := binary.LittleEndian.Uint16(buf[20:22])
	gpsTime := math.Float64frombits(binary.LittleEndian.Uint64(buf[22:30]))

	// Clamp return numbers for legacy 3-bit fields (max 7)
	var rn, nr uint8
	if numReturns > 7 {
		nr = 7
		if returnNum > 6 {
			if returnNum >= numReturns {
				rn = 7
			} else {
				rn = 6
			}
		} else {
			rn = returnNum
		}
	} else {
		rn = returnNum
		nr = numReturns
	}

	// Compute scan angle rank: I8_CLAMP(I16_QUANTIZE(0.006f * scanAngle))
	sa := float32(scanAngle) * 0.006
	var saClamped int32
	if sa >= 0 {
		saClamped = int32(sa + 0.5)
	} else {
		saClamped = int32(sa - 0.5)
	}
	scanAngleRank := int8(i32ClampI8(saClamped))

	// Write LAStempReadPoint10 layout (40 bytes)
	binary.LittleEndian.PutUint32(item[0:4], uint32(x))
	binary.LittleEndian.PutUint32(item[4:8], uint32(y))
	binary.LittleEndian.PutUint32(item[8:12], uint32(z))
	binary.LittleEndian.PutUint16(item[12:14], intensity)

	// Byte 14: return_number:3 | number_of_returns:3 | scan_dir:1 | eofl:1
	item[14] = (rn & 0x07) | ((nr & 0x07) << 3) | (scanDir << 6) | (eofl << 7)

	// Classification: flags go into bits 7-5, class goes into bits 4-0 (if < 32)
	class := (classFlags << 5) & 0xE0
	if classification < 32 {
		class |= classification
	}
	item[15] = class
	item[16] = byte(scanAngleRank)
	item[17] = userData
	binary.LittleEndian.PutUint16(item[18:20], pointSourceID)

	// Extended scan angle at bytes 20-21 (raw LAS 1.4 scan angle)
	binary.LittleEndian.PutUint16(item[20:22], uint16(scanAngle))

	// Byte 22: extended_point_type:2 | extended_scanner_channel:2 | extended_classification_flags:4
	item[22] = ((scannerCh & 0x03) << 2) | ((classFlags & 0x0F) << 4)

	// Byte 23: extended classification
	item[23] = classification

	// Byte 24: extended_return_number:4 | extended_number_of_returns:4
	item[24] = (returnNum & 0x0F) | ((numReturns & 0x0F) << 4)

	// Bytes 25-27: dummy padding (already zero from make)
	// Bytes 28-31: deleted_flag (leave as zero)

	// GPS time at bytes 32-39 (8-byte aligned per C++ struct)
	binary.LittleEndian.PutUint64(item[32:40], math.Float64bits(gpsTime))

	return nil
}

func i32ClampI8(v int32) int32 {
	if v < -128 {
		return -128
	}
	if v > 127 {
		return 127
	}
	return v
}

// ---------------------------------------------------------------------------
// RGB14 — 6-byte raw reader (3×uint16, same layout as RGB12 for uncompressed)
// ---------------------------------------------------------------------------

type LASreadItemRawRGB14LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawRGB14LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:6])
}

// ---------------------------------------------------------------------------
// RGBNIR14 — 8-byte raw reader (R+G+B+NIR, 4×uint16)
// ---------------------------------------------------------------------------

type LASreadItemRawRGBNIR14LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawRGBNIR14LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:8])
}

// ---------------------------------------------------------------------------
// WAVEPACKET14 — 29-byte raw reader
// ---------------------------------------------------------------------------

type LASreadItemRawWavepacket14LE struct {
	LASreadItemRaw
}

func (r *LASreadItemRawWavepacket14LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:29])
}

// ---------------------------------------------------------------------------
// BYTE14 — variable-size raw reader (extra bytes for types 6-10)
// ---------------------------------------------------------------------------

type LASreadItemRawByte14LE struct {
	LASreadItemRaw
	number uint32
}

func NewLASreadItemRawByte14LE(number uint32) *LASreadItemRawByte14LE {
	return &LASreadItemRawByte14LE{number: number}
}

func (r *LASreadItemRawByte14LE) Read(item []byte, _ *uint32) error {
	return r.instream.GetBytes(item[:r.number])
}

var errBufTooSmall = errBufTooSmallType{}

type errBufTooSmallType struct{}

func (e errBufTooSmallType) Error() string { return "buffer too small" }
