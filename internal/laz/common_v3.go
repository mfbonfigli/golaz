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
// common_v3.go — v3/v4 shared context structures and lookup tables ported
// from src/laszip_common_v3.hpp. Read-relevant subset only.
package laz

// LAScontextPOINT14 holds the shared decompression context for POINT14 items
// in v3 and v4 layered compression. All ArithmeticModel* and IntegerCompressor*
// pointers from C++ become pointer fields in Go.
type LAScontextPOINT14 struct {
	Unused bool

	LastItem      [128]uint8
	LastIntensity [8]uint16
	LastXDiffMed5 [12]*StreamingMedian5
	LastYDiffMed5 [12]*StreamingMedian5
	LastZ         [8]int32

	MChangedValues   [8]*ArithmeticModel
	MScannerChannel  *ArithmeticModel
	MNumberOfReturns [16]*ArithmeticModel
	MReturnNumberGPS *ArithmeticModel
	MReturnNumber    [16]*ArithmeticModel
	IcDX             *IntegerCompressor
	IcDY             *IntegerCompressor
	IcZ              *IntegerCompressor

	MClassification [64]*ArithmeticModel

	MFlags [64]*ArithmeticModel

	MUserData [64]*ArithmeticModel

	IcIntensity *IntegerCompressor

	IcScanAngle *IntegerCompressor

	IcPointSourceID *IntegerCompressor

	// GPS time context
	Last                uint32
	Next                uint32
	LastGPSTime         [4]uint64
	LastGPSTimeDiff     [4]int32
	MultiExtremeCounter [4]int32
	GPSTimeChange       bool // gps_time_change flag (C++: LASpoint14::gps_time_change)

	MGPSTimeMulti *ArithmeticModel
	MGPSTime0Diff *ArithmeticModel
	IcGPSTime     *IntegerCompressor
}

// LAScontextRGB14 holds the shared decompression context for RGB14 items
// in v3 and v4 layered compression.
type LAScontextRGB14 struct {
	Unused bool

	LastItem [3]uint16

	MByteUsed *ArithmeticModel
	MRGBDiff0 *ArithmeticModel
	MRGBDiff1 *ArithmeticModel
	MRGBDiff2 *ArithmeticModel
	MRGBDiff3 *ArithmeticModel
	MRGBDiff4 *ArithmeticModel
	MRGBDiff5 *ArithmeticModel
}

// LAScontextRGBNIR14 holds the shared decompression context for RGBNIR14 items
// in v3 and v4 layered compression.
type LAScontextRGBNIR14 struct {
	Unused bool

	LastItem [4]uint16

	MRGBBytesUsed *ArithmeticModel
	MRGBDiff0     *ArithmeticModel
	MRGBDiff1     *ArithmeticModel
	MRGBDiff2     *ArithmeticModel
	MRGBDiff3     *ArithmeticModel
	MRGBDiff4     *ArithmeticModel
	MRGBDiff5     *ArithmeticModel

	MNIRBytesUsed *ArithmeticModel
	MNIRDiff0     *ArithmeticModel
	MNIRDiff1     *ArithmeticModel
}

// LAScontextWAVEPACKET14 holds the shared decompression context for
// WAVEPACKET14 items in v3 and v4 layered compression.
type LAScontextWAVEPACKET14 struct {
	Unused bool

	LastItem          [29]uint8
	LastDiff32        int32
	SymLastOffsetDiff uint32

	MPacketIndex  *ArithmeticModel
	MOffsetDiff   [4]*ArithmeticModel
	IcOffsetDiff  *IntegerCompressor
	IcPacketSize  *IntegerCompressor
	IcReturnPoint *IntegerCompressor
	IcXYZ         *IntegerCompressor
}

// LAScontextBYTE14 holds the shared decompression context for BYTE14 items
// in v3 and v4 layered compression.
type LAScontextBYTE14 struct {
	Unused bool

	LastItem []uint8
	MBytes   []*ArithmeticModel
}

// numberReturnMap6ctx maps (number_of_returns, return_number) to a context
// index (0–5) for v3/v4 compression. 16×16 table from laszip_common_v3.hpp.
var NumberReturnMap6ctx = [16][16]uint8{
	{0, 1, 2, 3, 4, 5, 3, 4, 4, 5, 5, 5, 5, 5, 5, 5},
	{1, 0, 1, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3},
	{2, 1, 2, 4, 4, 4, 4, 4, 4, 4, 4, 3, 3, 3, 3, 3},
	{3, 3, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
	{4, 3, 4, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
	{5, 3, 4, 4, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4},
	{3, 3, 4, 4, 4, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4, 4},
	{4, 3, 4, 4, 4, 4, 4, 5, 4, 4, 4, 4, 4, 4, 4, 4},
	{4, 3, 4, 4, 4, 4, 4, 4, 5, 4, 4, 4, 4, 4, 4, 4},
	{5, 3, 4, 4, 4, 4, 4, 4, 4, 5, 4, 4, 4, 4, 4, 4},
	{5, 3, 4, 4, 4, 4, 4, 4, 4, 4, 5, 4, 4, 4, 4, 4},
	{5, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 4, 4, 4},
	{5, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 5, 4, 4},
	{5, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 5, 4},
	{5, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5, 5},
	{5, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 5, 5},
}

// numberReturnLevel8ctx maps (number_of_returns, return_number) to a
// penetration level context (0–7) for v3/v4 compression.
// 16×16 table from laszip_common_v3.hpp.
var NumberReturnLevel8ctx = [16][16]uint8{
	{0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7, 7, 7, 7, 7},
	{1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7, 7, 7, 7},
	{2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7, 7, 7},
	{3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7, 7},
	{4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7, 7},
	{5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7, 7},
	{6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7, 7},
	{7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7, 7},
	{7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6, 7},
	{7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5, 6},
	{7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4, 5},
	{7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3, 4},
	{7, 7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2, 3},
	{7, 7, 7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1, 2},
	{7, 7, 7, 7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0, 1},
	{7, 7, 7, 7, 7, 7, 7, 7, 7, 6, 5, 4, 3, 2, 1, 0},
}
