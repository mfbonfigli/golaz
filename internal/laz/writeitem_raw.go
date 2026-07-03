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

// writeitem_raw.go — raw (uncompressed) point item writers, ported from
// src/laswriteitemraw.hpp. Only LE writers are implemented (the dominant
// LAS encoding), mirroring the raw readers in readitem_raw.go.
package laz

import (
	"encoding/binary"
)

// ---------------------------------------------------------------------------
// POINT10 — 20-byte raw writer (LAS point types 0-5)
// ---------------------------------------------------------------------------

type LASwriteItemRawPoint10LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawPoint10LE) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:20])
}

// ---------------------------------------------------------------------------
// GPSTIME11 — 8-byte raw writer
// ---------------------------------------------------------------------------

type LASwriteItemRawGpsTime11LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawGpsTime11LE) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:8])
}

// ---------------------------------------------------------------------------
// RGB12 — 6-byte raw writer (3×uint16). Also used for RGB14 items (the C++
// laswritepoint.cpp dispatch reuses LASwriteItemRaw_RGB12 for both).
// ---------------------------------------------------------------------------

type LASwriteItemRawRGB12LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawRGB12LE) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:6])
}

// ---------------------------------------------------------------------------
// WAVEPACKET13 — 29-byte raw writer. Also used for WAVEPACKET14 items.
// ---------------------------------------------------------------------------

type LASwriteItemRawWavepacket13LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawWavepacket13LE) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:29])
}

// ---------------------------------------------------------------------------
// BYTE — variable-size raw writer (extra bytes). The C++ uses one class
// (LASwriteItemRaw_BYTE) for both BYTE and BYTE14 items.
// ---------------------------------------------------------------------------

type LASwriteItemRawByte struct {
	LASwriteItemRaw
	number uint32
}

func NewLASwriteItemRawByte(number uint32) *LASwriteItemRawByte {
	return &LASwriteItemRawByte{number: number}
}

func (w *LASwriteItemRawByte) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:w.number])
}

// ---------------------------------------------------------------------------
// POINT14 — 40-byte in-memory LAStempWritePoint10 layout → 30-byte on-disk
// PDRF6 record. Exact inverse of LASreadItemRawPoint14LE (readitem_raw.go),
// ported from C++ LASwriteItemRaw_POINT14_LE (laswriteitemraw.hpp).
//
// In-memory layout (LAStempWritePoint10, LE — see readitem_raw.go):
//   [ 0.. 3] X (I32)
//   [ 4.. 7] Y (I32)
//   [ 8..11] Z (I32)
//   [12..13] intensity (U16)
//   [14]     return_number:3 | number_of_returns:3 | scan_direction_flag:1 | eofl:1
//   [15]     classification (merged: flags<<5 | class if class<32)
//   [16]     scan_angle_rank (I8)
//   [17]     user_data (U8)
//   [18..19] point_source_ID (U16)
//   [20..21] extended_scan_angle (I16)
//   [22]     extended_point_type:2 | extended_scanner_channel:2 | extended_classification_flags:4
//   [23]     extended_classification (U8)
//   [24]     extended_return_number:4 | extended_number_of_returns:4
//   [25..27] dummy[3]
//   [28..31] deleted_flag (U32)
//   [32..39] gps_time (F64)
//
// On-disk 30-byte LAS 1.4 point payload:
//   [ 0.. 3] X, [ 4.. 7] Y, [ 8..11] Z
//   [12..13] intensity
//   [14]     return_number:4 | number_of_returns:4
//   [15]     classification_flags:4 | scanner_channel:2 | scan_direction_flag:1 | eofl:1
//   [16]     classification
//   [17]     user_data
//   [18..19] scan_angle (I16)
//   [20..21] point_source_ID
//   [22..29] gps_time (F64)
// ---------------------------------------------------------------------------

// i16Quantize mirrors C++ I16_QUANTIZE on a float32 value:
// v >= 0 ? (I16)(v+0.5f) : (I16)(v-0.5f). The additions happen in float32
// and the conversion truncates toward zero, exactly like the C++ cast.
func i16Quantize(n float32) int16 {
	if n >= 0 {
		return int16(n + 0.5)
	}
	return int16(n - 0.5)
}

type LASwriteItemRawPoint14LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawPoint14LE) Write(item []byte, _ *uint32) error {
	if len(item) < 40 {
		return errBufTooSmall
	}
	var buf [30]byte

	copy(buf[0:12], item[0:12])   // X, Y, Z
	copy(buf[12:14], item[12:14]) // intensity

	scanDir := (item[14] >> 6) & 0x01
	eofl := (item[14] >> 7) & 0x01
	classification := item[15] & 31
	userData := item[17]

	var classFlags, scannerCh, rn, nr uint8
	var scanAngle int16
	if item[22]&0x03 != 0 { // extended_point_type
		// classification_flags = (extended_classification_flags & 8) | (classification >> 5)
		classFlags = ((item[22] >> 4) & 0x08) | (item[15] >> 5)
		if classification == 0 {
			classification = item[23] // extended_classification
		}
		scannerCh = (item[22] >> 2) & 0x03
		rn = item[24] & 0x0F        // extended_return_number
		nr = (item[24] >> 4) & 0x0F // extended_number_of_returns
		scanAngle = int16(binary.LittleEndian.Uint16(item[20:22]))
	} else {
		classFlags = item[15] >> 5
		scannerCh = 0
		rn = item[14] & 0x07
		nr = (item[14] >> 3) & 0x07
		// scan_angle = I16_QUANTIZE(scan_angle_rank / 0.006f)
		scanAngle = i16Quantize(float32(int8(item[16])) / 0.006)
	}

	buf[14] = (rn & 0x0F) | (nr << 4)
	buf[15] = (classFlags & 0x0F) | ((scannerCh & 0x03) << 4) | (scanDir << 6) | (eofl << 7)
	buf[16] = classification
	buf[17] = userData
	binary.LittleEndian.PutUint16(buf[18:20], uint16(scanAngle))
	copy(buf[20:22], item[18:20]) // point_source_ID
	copy(buf[22:30], item[32:40]) // gps_time

	return w.outstream.PutBytes(buf[:])
}

// ---------------------------------------------------------------------------
// RGBNIR14 — 8-byte raw writer (R+G+B+NIR, 4×uint16)
// ---------------------------------------------------------------------------

type LASwriteItemRawRGBNIR14LE struct {
	LASwriteItemRaw
}

func (w *LASwriteItemRawRGBNIR14LE) Write(item []byte, _ *uint32) error {
	return w.outstream.PutBytes(item[:8])
}
