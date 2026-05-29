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
// types.go — Platform types, endian-swap helpers, clamp/fold macros, min/max,
// popcount, float comparison ported from src/mydefs.hpp.
package laz

import (
	"encoding/binary"
	"math"
	"math/bits"
)

// IsLittleEndian reports whether the native byte order is little-endian.
var IsLittleEndian = binary.ByteOrder(binary.NativeEndian) == binary.LittleEndian

// ---------------------------------------------------------------------------
// Endian-swap helpers (port of ENDIAN_SWAP_16_, ENDIAN_SWAP_32_, ENDIAN_SWAP_64_)
// These operate on []byte slices in-place. The C++ versions work on U8* fields.
// ---------------------------------------------------------------------------

// endianSwap16 reverses the byte order of a 2-byte field in-place.
func endianSwap16(field []byte) {
	field[0], field[1] = field[1], field[0]
}

// endianSwap32 reverses the byte order of a 4-byte field in-place.
func endianSwap32(field []byte) {
	field[0], field[3] = field[3], field[0]
	field[1], field[2] = field[2], field[1]
}

// endianSwap64 reverses the byte order of an 8-byte field in-place.
func endianSwap64(field []byte) {
	field[0], field[7] = field[7], field[0]
	field[1], field[6] = field[6], field[1]
	field[2], field[5] = field[5], field[2]
	field[3], field[4] = field[4], field[3]
}

// ---------------------------------------------------------------------------
// U8_FOLD — C++: #define U8_FOLD(n) (((n) < U8_MIN) ? (n + U8_MAX_PLUS_ONE) :
//
//	(((n) > U8_MAX) ? (n - U8_MAX_PLUS_ONE) : (n)))
//
// ---------------------------------------------------------------------------

func u8Fold(n int) uint8 {
	const (
		u8Min        = 0
		u8Max        = 255
		u8MaxPlusOne = 256
	)
	if n < u8Min {
		return uint8(n + u8MaxPlusOne)
	}
	if n > u8Max {
		return uint8(n - u8MaxPlusOne)
	}
	return uint8(n)
}

// ---------------------------------------------------------------------------
// Clamp macros → typed Go functions
// ---------------------------------------------------------------------------

func i32Clamp(n int) int32 {
	const i32Min int = math.MinInt32
	const i32Max int = math.MaxInt32
	if n <= i32Min {
		return int32(i32Min)
	}
	if n >= i32Max {
		return int32(i32Max)
	}
	return int32(n)
}

func u32Clamp(n uint32) uint32 {
	const u32Min uint32 = 0
	const u32Max uint32 = 0xFFFFFFFF
	// U32_CLAMP in C++: (((n) <= U32_MIN) ? U32_MIN : (((n) >= U32_MAX) ? U32_MAX : ((U32)(n))))
	if n <= u32Min {
		return u32Min
	}
	if n >= u32Max {
		return u32Max
	}
	return n
}

// ---------------------------------------------------------------------------
// Popcount (C++ uses compiler intrinsics; Go has math/bits)
// ---------------------------------------------------------------------------

// popcount returns the number of set bits in x.
func popcount(x uint32) int {
	return bits.OnesCount32(x)
}

// ---------------------------------------------------------------------------
// Min / Max — typed helpers (port of MIN2, MAX2, MIN3, MAX3)
// ---------------------------------------------------------------------------

func min2[T ~int32 | ~uint32 | ~int | ~uint](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func max2[T ~int32 | ~uint32 | ~int | ~uint](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func min3[T ~int32 | ~uint32 | ~int | ~uint](a, b, c T) T {
	return min2(min2(a, b), c)
}

func max3[T ~int32 | ~uint32 | ~int | ~uint](a, b, c T) T {
	return max2(max2(a, b), c)
}

// ---------------------------------------------------------------------------
// Float comparison (port of FLOATEQUAL and fp_equal<T>)
// ---------------------------------------------------------------------------

// floatEqual returns true if |a - b| < 1e-8 (C++ FLOATEQUAL macro).
func floatEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-8
}

// fpEqualFloat32 returns true if two float32 values are approximately equal (eps = 1e-6).
func fpEqualFloat32(a, b float32) bool {
	return math.Abs(float64(a)-float64(b)) < 1e-6
}

// fpEqualFloat64 returns true if two float64 values are approximately equal (eps = 1e-12).
func fpEqualFloat64(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

// ---------------------------------------------------------------------------
// Additional floor/ceil macros (from mydefs.hpp)
// These are used in integer compression logic.
// ---------------------------------------------------------------------------

func i16Floor(n float64) int16 {
	v := int16(n)
	if float64(v) > n {
		return v - 1
	}
	return v
}

func i32Floor(n float64) int32 {
	v := int32(n)
	if float64(v) > n {
		return v - 1
	}
	return v
}

func i64Floor(n float64) int64 {
	v := int64(n)
	if float64(v) > n {
		return v - 1
	}
	return v
}

func boolU32(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// Size constants ported from C++ #defines
// ---------------------------------------------------------------------------

const (
	U8_MAX          = 255
	U8_MAX_PLUS_ONE = 256
	U16_MAX         = 65535
	U32_MAX         = 0xFFFFFFFF
	I32_MIN         = math.MinInt32
	I32_MAX         = math.MaxInt32
)
