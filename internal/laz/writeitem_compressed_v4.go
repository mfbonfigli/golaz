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

// writeitem_compressed_v4.go — v4 layered (LAYERED_CHUNKED) item writers,
// ported from src/laswriteitemcompressed_v4.cpp. The v4 writers share the
// v3 cores (writeitem_compressed_v3.go) and differ ONLY in scanner-channel
// context propagation (verified by diffing the C++ v3/v4 sources):
//
//   - POINT14: v4 sets the shared *context unconditionally after the
//     scanner-channel-changed branch (C++ v4.cpp:576); v3 sets it only
//     inside that branch.
//   - RGB14/RGBNIR14/WAVEPACKET14/BYTE14: on a context switch, v4 rebinds
//     last_item to the new context unconditionally; v3 rebinds only when
//     the new context was unused (stale last_item behavior).
//
// The Go readers replicate both behaviors per version
// (readitem_compressed_v3.go vs readitem_compressed_v4.go); the writers
// must too, or multi-scanner-channel files would be corrupt.
package laz

// LASwriteItemCompressedPoint14v4 is the v4 layered POINT14 writer.
type LASwriteItemCompressedPoint14v4 struct{ *point14v34Writer }

// NewLASwriteItemCompressedPoint14v4 creates a v4 layered POINT14 writer.
// enc is the dummy encoder that provides access to the main output stream.
func NewLASwriteItemCompressedPoint14v4(enc *ArithmeticEncoder) *LASwriteItemCompressedPoint14v4 {
	return &LASwriteItemCompressedPoint14v4{newPoint14v34Writer(enc, true)}
}

// LASwriteItemCompressedRGB14v4 is the v4 layered RGB14 writer.
type LASwriteItemCompressedRGB14v4 struct{ *rgb14v34Writer }

// NewLASwriteItemCompressedRGB14v4 creates a v4 layered RGB14 writer.
func NewLASwriteItemCompressedRGB14v4(enc *ArithmeticEncoder) *LASwriteItemCompressedRGB14v4 {
	return &LASwriteItemCompressedRGB14v4{newRGB14v34Writer(enc, true)}
}

// LASwriteItemCompressedRGBNIR14v4 is the v4 layered RGBNIR14 writer.
type LASwriteItemCompressedRGBNIR14v4 struct{ *rgbnir14v34Writer }

// NewLASwriteItemCompressedRGBNIR14v4 creates a v4 layered RGBNIR14 writer.
func NewLASwriteItemCompressedRGBNIR14v4(enc *ArithmeticEncoder) *LASwriteItemCompressedRGBNIR14v4 {
	return &LASwriteItemCompressedRGBNIR14v4{newRGBNIR14v34Writer(enc, true)}
}

// LASwriteItemCompressedWavepacket14v4 is the v4 layered WAVEPACKET14 writer.
type LASwriteItemCompressedWavepacket14v4 struct{ *wavepacket14v34Writer }

// NewLASwriteItemCompressedWavepacket14v4 creates a v4 layered WAVEPACKET14 writer.
func NewLASwriteItemCompressedWavepacket14v4(enc *ArithmeticEncoder) *LASwriteItemCompressedWavepacket14v4 {
	return &LASwriteItemCompressedWavepacket14v4{newWavepacket14v34Writer(enc, true)}
}

// LASwriteItemCompressedByte14v4 is the v4 layered BYTE14 writer.
type LASwriteItemCompressedByte14v4 struct{ *byte14v34Writer }

// NewLASwriteItemCompressedByte14v4 creates a v4 layered BYTE14 writer for
// `number` extra bytes.
func NewLASwriteItemCompressedByte14v4(enc *ArithmeticEncoder, number uint32) *LASwriteItemCompressedByte14v4 {
	return &LASwriteItemCompressedByte14v4{newByte14v34Writer(enc, number, true)}
}

// Interface conformance checks.
var (
	_ LASwriteItemCompressed = (*LASwriteItemCompressedPoint14v4)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedRGB14v4)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedRGBNIR14v4)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedWavepacket14v4)(nil)
	_ LASwriteItemCompressed = (*LASwriteItemCompressedByte14v4)(nil)
)
