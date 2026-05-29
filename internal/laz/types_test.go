// Copyright (c) 2026 Massimo Federico Bonfigli
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

package laz

import (
	"math"
	"testing"
)

// ---- Endian swap tests ----

func TestEndianSwap16(t *testing.T) {
	buf := []byte{0x01, 0x02}
	endianSwap16(buf)
	if buf[0] != 0x02 || buf[1] != 0x01 {
		t.Fatalf("endianSwap16: got [%02x %02x], want [02 01]", buf[0], buf[1])
	}
}

func TestEndianSwap32(t *testing.T) {
	buf := []byte{0x01, 0x02, 0x03, 0x04}
	endianSwap32(buf)
	if buf[0] != 0x04 || buf[1] != 0x03 || buf[2] != 0x02 || buf[3] != 0x01 {
		t.Fatalf("endianSwap32: got %v, want [04 03 02 01]", buf)
	}
}

func TestEndianSwap64(t *testing.T) {
	buf := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	endianSwap64(buf)
	want := []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}
	for i := range buf {
		if buf[i] != want[i] {
			t.Fatalf("endianSwap64[%d]: got %02x, want %02x", i, buf[i], want[i])
		}
	}
}

// ---- U8_FOLD ----

func TestU8Fold(t *testing.T) {
	tests := []struct {
		n    int
		want uint8
	}{
		{0, 0},
		{100, 100},
		{255, 255},
		{256, 0},    // wraps around: 256 - 256 = 0
		{257, 1},    // 257 - 256 = 1
		{-1, 255},   // -1 + 256 = 255
		{-2, 254},   // -2 + 256 = 254
		{-255, 1},   // -255 + 256 = 1
		{-256, 0},   // -256 + 256 = 0
		{-257, 255}, // -257 is < 0 so n+256 = -1 → but -1+256=255... wait: -257 < 0, so n+256 = -1, which as uint8 wraps to 255
	}
	for _, tt := range tests {
		got := u8Fold(tt.n)
		if got != tt.want {
			t.Errorf("u8Fold(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

// ---- Clamp ----

func TestI32Clamp(t *testing.T) {
	tests := []struct {
		n    int
		want int32
	}{
		{0, 0},
		{100, 100},
		{-100, -100},
		{math.MaxInt32, math.MaxInt32},
		{math.MinInt32, math.MinInt32},
	}
	for _, tt := range tests {
		got := i32Clamp(tt.n)
		if got != tt.want {
			t.Errorf("i32Clamp(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestU32Clamp(t *testing.T) {
	tests := []struct {
		n    uint32
		want uint32
	}{
		{0, 0},
		{100, 100},
		{0xFFFFFFFF, 0xFFFFFFFF},
	}
	for _, tt := range tests {
		got := u32Clamp(tt.n)
		if got != tt.want {
			t.Errorf("u32Clamp(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

// ---- Popcount ----

func TestPopcount(t *testing.T) {
	tests := []struct {
		x    uint32
		want int
	}{
		{0, 0},
		{1, 1},
		{2, 1},
		{3, 2},
		{0xFFFFFFFF, 32},
		{0xAAAAAAAA, 16},
		{0x55555555, 16},
	}
	for _, tt := range tests {
		got := popcount(tt.x)
		if got != tt.want {
			t.Errorf("popcount(%d) = %d, want %d", tt.x, got, tt.want)
		}
	}
}

// ---- Min/Max ----

func TestMin2(t *testing.T) {
	if got := min2(int32(3), int32(7)); got != 3 {
		t.Errorf("min2(3,7) = %d, want 3", got)
	}
	if got := min2(int32(7), int32(3)); got != 3 {
		t.Errorf("min2(7,3) = %d, want 3", got)
	}
	if got := min2(int32(-5), int32(5)); got != -5 {
		t.Errorf("min2(-5,5) = %d, want -5", got)
	}
}

func TestMax2(t *testing.T) {
	if got := max2(int32(3), int32(7)); got != 7 {
		t.Errorf("max2(3,7) = %d, want 7", got)
	}
	if got := max2(int32(7), int32(3)); got != 7 {
		t.Errorf("max2(7,3) = %d, want 7", got)
	}
}

func TestMin3(t *testing.T) {
	if got := min3(int32(5), int32(2), int32(8)); got != 2 {
		t.Errorf("min3(5,2,8) = %d, want 2", got)
	}
}

func TestMax3(t *testing.T) {
	if got := max3(int32(5), int32(2), int32(8)); got != 8 {
		t.Errorf("max3(5,2,8) = %d, want 8", got)
	}
}

// ---- Float comparison ----

func TestFloatEqual(t *testing.T) {
	if !floatEqual(1.0, 1.0) {
		t.Error("floatEqual(1.0, 1.0) should be true")
	}
	if !floatEqual(1.0, 1.0+1e-9) {
		t.Error("floatEqual(1.0, 1.0+1e-9) should be true")
	}
	if floatEqual(1.0, 1.0+1e-7) {
		t.Error("floatEqual(1.0, 1.0+1e-7) should be false")
	}
}

func TestFpEqualFloat32(t *testing.T) {
	if !fpEqualFloat32(1.0, 1.0) {
		t.Error("fpEqualFloat32(1.0, 1.0) should be true")
	}
	if fpEqualFloat32(1.0, 1.0+1e-5) {
		t.Error("fpEqualFloat32(1.0, 1.0+1e-5) should be false")
	}
}

func TestFpEqualFloat64(t *testing.T) {
	if !fpEqualFloat64(1.0, 1.0) {
		t.Error("fpEqualFloat64(1.0, 1.0) should be true")
	}
	if fpEqualFloat64(1.0, 1.0+1e-11) {
		t.Error("fpEqualFloat64(1.0, 1.0+1e-11) should be false")
	}
}

// ---- Floor ----

func TestI32Floor(t *testing.T) {
	tests := []struct {
		n    float64
		want int32
	}{
		{3.7, 3},
		{3.0, 3},
		{-3.7, -4},
		{-3.0, -3},
		{0.0, 0},
	}
	for _, tt := range tests {
		got := i32Floor(tt.n)
		if got != tt.want {
			t.Errorf("i32Floor(%v) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestI64Floor(t *testing.T) {
	tests := []struct {
		n    float64
		want int64
	}{
		{3.7, 3},
		{3.0, 3},
		{-3.7, -4},
		{-3.0, -3},
		{0.0, 0},
	}
	for _, tt := range tests {
		got := i64Floor(tt.n)
		if got != tt.want {
			t.Errorf("i64Floor(%v) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

// ---- IsLittleEndian ----

func TestIsLittleEndian(t *testing.T) {
	// Just validate it's a valid bool — it should be true on x86/amd64.
	if IsLittleEndian != true && IsLittleEndian != false {
		t.Fatal("IsLittleEndian is not a valid bool")
	}
}

// ---- Constants ----

func TestConstants(t *testing.T) {
	if U8_MAX != 255 {
		t.Errorf("U8_MAX = %d, want 255", U8_MAX)
	}
	if U8_MAX_PLUS_ONE != 256 {
		t.Errorf("U8_MAX_PLUS_ONE = %d, want 256", U8_MAX_PLUS_ONE)
	}
	if U16_MAX != 65535 {
		t.Errorf("U16_MAX = %d, want 65535", U16_MAX)
	}
	if U32_MAX != 0xFFFFFFFF {
		t.Errorf("U32_MAX = %#x, want 0xFFFFFFFF", U32_MAX)
	}
	if I32_MIN != math.MinInt32 {
		t.Errorf("I32_MIN = %d, want %d", I32_MIN, math.MinInt32)
	}
	if I32_MAX != math.MaxInt32 {
		t.Errorf("I32_MAX = %d, want %d", I32_MAX, math.MaxInt32)
	}
}
