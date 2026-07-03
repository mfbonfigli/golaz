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
	"encoding/binary"
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// readitem interface tests
// ---------------------------------------------------------------------------

func TestLASreadItemRawInit(t *testing.T) {
	base := LASreadItemRaw{}
	err := base.Init(nil)
	if err != ErrNilStream {
		t.Errorf("Init(nil): got %v, want ErrNilStream", err)
	}
	arr := NewByteStreamInArray([]byte{0x01, 0x02, 0x03})
	err = base.Init(arr)
	if err != nil {
		t.Errorf("Init(arr): unexpected error %v", err)
	}
}

// ---------------------------------------------------------------------------
// POINT10 raw reader — 20 bytes
// ---------------------------------------------------------------------------

func TestReadItemRawPoint10LE(t *testing.T) {
	src := make([]byte, 20)
	binary.LittleEndian.PutUint32(src[0:4], 0x12345678)
	binary.LittleEndian.PutUint32(src[4:8], 0x9ABCDEF0)
	binary.LittleEndian.PutUint32(src[8:12], 0x0A0B0C0D)
	binary.LittleEndian.PutUint16(src[12:14], 0x4242)
	src[14] = 0x12 // return:1 num:2
	src[15] = 0x05 // classification=5
	src[16] = 0x80 // scan_angle_rank=-128
	src[17] = 0xFF // user_data=255
	binary.LittleEndian.PutUint16(src[18:20], 0xABCD)

	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawPoint10LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}

	out := make([]byte, 20)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// GPSTIME11 raw reader — 8 bytes
// ---------------------------------------------------------------------------

func TestReadItemRawGpsTime11LE(t *testing.T) {
	src := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawGpsTime11LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 8)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// RGB12 raw reader — 6 bytes
// ---------------------------------------------------------------------------

func TestReadItemRawRGB12LE(t *testing.T) {
	src := []byte{0x10, 0x20, 0x30, 0x40, 0x50, 0x60}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawRGB12LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 6)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// WAVEPACKET13 raw reader — 29 bytes
// ---------------------------------------------------------------------------

func TestReadItemRawWavepacket13LE(t *testing.T) {
	src := make([]byte, 29)
	for i := range src {
		src[i] = byte(i)
	}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawWavepacket13LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 29)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// BYTE variable-size raw reader
// ---------------------------------------------------------------------------

func TestReadItemRawByte(t *testing.T) {
	src := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	arr := NewByteStreamInArray(src)
	r := NewLASreadItemRawByte(5)
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 5)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// POINT14 raw reader — 40-byte C++ LAStempReadPoint10 layout
// ---------------------------------------------------------------------------

func TestReadItemRawPoint14LE(t *testing.T) {
	type testCase struct {
		X, Y, Z               int32
		Intensity             uint16
		ReturnNum, NumReturns uint8
		ClassFlags, ScannerCh uint8
		ScanDir, EdgeFlight   uint8
		Classification        uint8
		UserData              uint8
		ScanAngle             int16
		PointSourceID         uint16
		GPSTime               float64
		// Expected values in 40-byte LAStempReadPoint10 output
		ExpByte14   uint8 // return_number:3|nreturns:3|scan_dir:1|eofl:1
		ExpClass    uint8 // merged classification
		ExpSARank   int8  // scan angle rank
		ExpByte22   uint8 // ext_point_type(=1):2|scanner_ch:2|class_flags:4
		ExpExtClass uint8 // extended classification (byte 23)
		ExpByte24   uint8 // extended rn:4|extended nr:4 (byte 24)
	}

	tests := []testCase{
		// TC1: simple valid 3-bit returns
		{1000, 2000, 500, 128, 1, 3, 0, 0, 0, 0, 5, 42, -30, 1234, 123456789.0,
			0x19, 0x05, 0, 0x01, 5, 0x31},
		// TC2: num_returns > 7 clamped, rn=1 < nr so rn=1 nr=7
		{0, 0, 0, 0, 1, 9, 0, 0, 0, 0, 0, 0, 0, 0, 0.0,
			0x39, 0x00, 0, 0x01, 0, 0x91},
		// TC3: rn=7 nr=8 → nr=7, rn=6
		{0, 0, 0, 0, 7, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0.0,
			0x3E, 0x00, 0, 0x01, 0, 0x87},
		// TC4: rn=8 nr=8 → nr=7, rn=7 (since rn >= nr)
		{0, 0, 0, 0, 8, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0.0,
			0x3F, 0x00, 0, 0x01, 0, 0x88},
		// TC5: rn=0 nr=3 pass-through
		{0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0.0,
			0x18, 0x00, 0, 0x01, 0, 0x30},
		// TC6: scan_direction=1, eofl=1
		{0, 0, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 0.0,
			0xC9, 0x00, 0, 0x01, 0, 0x11},
		// TC7: class_flags=3, classification=63 (>=32, not merged)
		{0, 0, 0, 0, 1, 1, 3, 0, 0, 0, 63, 0, 0, 0, 0.0,
			0x09, 0x60, 0, 0x31, 63, 0x11},
		// TC8: scan_angle=-30000 → -180.0 → rank=-128
		{0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, -30000, 0, 0.0,
			0x09, 0x00, -128, 0x01, 0, 0x11},
		// TC9: max fields: scanner_ch=3, class_flags=15 → byte22=0xFD (incl. ext_point_type bit)
		{2147483647, 2147483647, 2147483647, 65535, 15, 15, 15, 3, 1, 1, 255, 255, 18000, 65535, 999999999.5,
			0xFF, 0xE0, 108, 0xFD, 255, 0xFF},
		// TC10: scanner_ch=1, class_flags=1 → byte22=0x15 (incl. ext_point_type bit)
		{500000, 4000000, 1500, 1024, 2, 4, 1, 1, 0, 0, 6, 128, 10, 56, 360000.123,
			0x22, 0x26, 0, 0x15, 6, 0x42},
	}

	for i, tc := range tests {
		src := make([]byte, 30)
		binary.LittleEndian.PutUint32(src[0:4], uint32(tc.X))
		binary.LittleEndian.PutUint32(src[4:8], uint32(tc.Y))
		binary.LittleEndian.PutUint32(src[8:12], uint32(tc.Z))
		binary.LittleEndian.PutUint16(src[12:14], tc.Intensity)
		src[14] = (tc.ReturnNum & 0x0F) | ((tc.NumReturns & 0x0F) << 4)
		src[15] = (tc.ClassFlags & 0x0F) | ((tc.ScannerCh & 0x03) << 4) | ((tc.ScanDir & 0x01) << 6) | ((tc.EdgeFlight & 0x01) << 7)
		src[16] = tc.Classification
		src[17] = tc.UserData
		binary.LittleEndian.PutUint16(src[18:20], uint16(tc.ScanAngle))
		binary.LittleEndian.PutUint16(src[20:22], tc.PointSourceID)
		binary.LittleEndian.PutUint64(src[22:30], math.Float64bits(tc.GPSTime))

		arr := NewByteStreamInArray(src)
		r := &LASreadItemRawPoint14LE{}
		if err := r.Init(arr); err != nil {
			t.Fatalf("tc%d: init error: %v", i, err)
		}
		out := make([]byte, 40)
		if err := r.Read(out, nil); err != nil {
			t.Fatalf("tc%d: read error: %v", i, err)
		}

		// Verify legacy fields
		if out[14] != tc.ExpByte14 {
			t.Errorf("tc%d: byte14 got %02x, want %02x", i, out[14], tc.ExpByte14)
		}
		if out[15] != tc.ExpClass {
			t.Errorf("tc%d: class got %02x, want %02x", i, out[15], tc.ExpClass)
		}
		if int8(out[16]) != tc.ExpSARank {
			t.Errorf("tc%d: scan_angle_rank got %d, want %d", i, int8(out[16]), tc.ExpSARank)
		}

		// Verify X, Y, Z, intensity passthrough
		if gotX := int32(binary.LittleEndian.Uint32(out[0:4])); gotX != tc.X {
			t.Errorf("tc%d: X got %d, want %d", i, gotX, tc.X)
		}
		if gotY := int32(binary.LittleEndian.Uint32(out[4:8])); gotY != tc.Y {
			t.Errorf("tc%d: Y got %d, want %d", i, gotY, tc.Y)
		}
		if gotZ := int32(binary.LittleEndian.Uint32(out[8:12])); gotZ != tc.Z {
			t.Errorf("tc%d: Z got %d, want %d", i, gotZ, tc.Z)
		}
		if gotI := binary.LittleEndian.Uint16(out[12:14]); gotI != tc.Intensity {
			t.Errorf("tc%d: intensity got %d, want %d", i, gotI, tc.Intensity)
		}
		// Extended scan angle at bytes 20-21
		if gotSA := int16(binary.LittleEndian.Uint16(out[20:22])); gotSA != tc.ScanAngle {
			t.Errorf("tc%d: ext_scan_angle got %d, want %d", i, gotSA, tc.ScanAngle)
		}
		// Byte 22: extended_point_type:2|scanner_ch:2|class_flags:4
		if out[22] != tc.ExpByte22 {
			t.Errorf("tc%d: byte22 got %02x, want %02x", i, out[22], tc.ExpByte22)
		}
		// Byte 23: extended classification
		if out[23] != tc.ExpExtClass {
			t.Errorf("tc%d: ext_class got %d, want %d", i, out[23], tc.ExpExtClass)
		}
		// Byte 24: extended_return_number:4|extended_number_of_returns:4
		if out[24] != tc.ExpByte24 {
			t.Errorf("tc%d: byte24 got %02x, want %02x", i, out[24], tc.ExpByte24)
		}
		// GPS time at bytes 32-40
		gotGPS := math.Float64frombits(binary.LittleEndian.Uint64(out[32:40]))
		if gotGPS != tc.GPSTime {
			t.Errorf("tc%d: gps_time at [32:40] got %f, want %f", i, gotGPS, tc.GPSTime)
		}
	}
}

// ---------------------------------------------------------------------------
// RGB14 raw reader
// ---------------------------------------------------------------------------

func TestReadItemRawRGB14LE(t *testing.T) {
	src := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawRGB14LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 6)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// RGBNIR14 raw reader
// ---------------------------------------------------------------------------

func TestReadItemRawRGBNIR14LE(t *testing.T) {
	src := []byte{0xAA, 0x00, 0xBB, 0x00, 0xCC, 0x00, 0xDD, 0x00}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawRGBNIR14LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 8)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// WAVEPACKET14 raw reader
// ---------------------------------------------------------------------------

func TestReadItemRawWavepacket14LE(t *testing.T) {
	src := make([]byte, 29)
	for i := range src {
		src[i] = byte(255 - i)
	}
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawWavepacket14LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 29)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// BYTE14 raw reader
// ---------------------------------------------------------------------------

func TestReadItemRawByte14LE(t *testing.T) {
	src := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
	arr := NewByteStreamInArray(src)
	r := NewLASreadItemRawByte14LE(7)
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 7)
	if err := r.Read(out, nil); err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if out[i] != src[i] {
			t.Errorf("byte[%d]: got %02x, want %02x", i, out[i], src[i])
		}
	}
}

// ---------------------------------------------------------------------------
// V3 lookup tables — verified against C++ laszip_common_v3.hpp
// ---------------------------------------------------------------------------

func TestV3LookupTables(t *testing.T) {
	tests := []struct {
		table string
		n, r  int
		want  uint8
	}{
		{"NumberReturnMap6ctx", 0, 0, 0},
		{"NumberReturnMap6ctx", 1, 1, 0},
		{"NumberReturnMap6ctx", 2, 2, 2},
		{"NumberReturnMap6ctx", 5, 5, 5},
		{"NumberReturnMap6ctx", 7, 7, 5},
		{"NumberReturnMap6ctx", 15, 15, 5},

		{"NumberReturnLevel8ctx", 0, 0, 0},
		{"NumberReturnLevel8ctx", 1, 1, 0},
		{"NumberReturnLevel8ctx", 5, 5, 0},
		{"NumberReturnLevel8ctx", 7, 7, 0},
		{"NumberReturnLevel8ctx", 10, 10, 0},
		{"NumberReturnLevel8ctx", 15, 0, 7},
		{"NumberReturnLevel8ctx", 15, 15, 0},
	}

	for _, tc := range tests {
		var got uint8
		if tc.table == "NumberReturnMap6ctx" {
			got = NumberReturnMap6ctx[tc.n][tc.r]
		} else {
			got = NumberReturnLevel8ctx[tc.n][tc.r]
		}
		if got != tc.want {
			t.Errorf("%s[%d][%d]: got %d, want %d", tc.table, tc.n, tc.r, got, tc.want)
		}
	}

	if len(NumberReturnMap6ctx) != 16 {
		t.Errorf("NumberReturnMap6ctx rows: got %d, want 16", len(NumberReturnMap6ctx))
	}
	for i := range 16 {
		if len(NumberReturnMap6ctx[i]) != 16 {
			t.Errorf("NumberReturnMap6ctx[%d] cols: got %d, want 16", i, len(NumberReturnMap6ctx[i]))
		}
	}
	if len(NumberReturnLevel8ctx) != 16 {
		t.Errorf("NumberReturnLevel8ctx rows: got %d, want 16", len(NumberReturnLevel8ctx))
	}
	for i := range 16 {
		if len(NumberReturnLevel8ctx[i]) != 16 {
			t.Errorf("NumberReturnLevel8ctx[%d] cols: got %d, want 16", i, len(NumberReturnLevel8ctx[i]))
		}
	}
}

// ---------------------------------------------------------------------------
// POINT14 buffer too small error
// ---------------------------------------------------------------------------

func TestReadItemRawPoint14LE_BufferTooSmall(t *testing.T) {
	src := make([]byte, 30)
	arr := NewByteStreamInArray(src)
	r := &LASreadItemRawPoint14LE{}
	if err := r.Init(arr); err != nil {
		t.Fatal(err)
	}
	small := make([]byte, 39)
	err := r.Read(small, nil)
	if err == nil {
		t.Error("expected error for buffer < 40 bytes, got nil")
	}
}
