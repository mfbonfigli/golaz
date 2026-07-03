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

// hardening_test.go — table-driven tests for spec conformance and
// malformed-input hardening of the public reader API. Expected behavior is
// derived from the LAS 1.0–1.4 specifications and the LASzip C++ reference
// implementation (laszip_dll.cpp reader path).
package golaz

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Synthetic LAS file builder
// ---------------------------------------------------------------------------

type lasSpec struct {
	versionMinor uint8
	headerSize   uint16 // 0 → default for version (227 / 235 / 375)
	globalEnc    uint16
	pointFormat  uint8 // raw byte, including compression bits if any
	recordLen    uint16
	numPoints    uint32
	vlrs         [][]byte // raw VLR bytes (54-byte header + payload each)
	points       []byte
	evlrs        [][]byte // raw EVLR bytes (60-byte header + payload each), LAS 1.4+
}

func defaultHeaderSize(minor uint8) uint16 {
	switch {
	case minor >= 4:
		return 375
	case minor == 3:
		return 235
	default:
		return 227
	}
}

// buildLAS assembles a synthetic LAS file byte stream.
func buildLAS(spec lasSpec) []byte {
	hs := spec.headerSize
	if hs == 0 {
		hs = defaultHeaderSize(spec.versionMinor)
	}
	// Build a full-size template (375 bytes) and truncate/pad to hs.
	tmpl := make([]byte, 375)
	copy(tmpl[0:4], "LASF")
	binary.LittleEndian.PutUint16(tmpl[6:8], spec.globalEnc)
	tmpl[24] = 1
	tmpl[25] = spec.versionMinor
	copy(tmpl[26:], "golaz test")
	copy(tmpl[58:], "golaz test")
	binary.LittleEndian.PutUint16(tmpl[90:92], 1)    // day
	binary.LittleEndian.PutUint16(tmpl[92:94], 2026) // year
	binary.LittleEndian.PutUint16(tmpl[94:96], hs)

	var vlrBytes []byte
	for _, v := range spec.vlrs {
		vlrBytes = append(vlrBytes, v...)
	}
	offsetToPoints := uint32(hs) + uint32(len(vlrBytes))
	binary.LittleEndian.PutUint32(tmpl[96:100], offsetToPoints)
	binary.LittleEndian.PutUint32(tmpl[100:104], uint32(len(spec.vlrs)))
	tmpl[104] = spec.pointFormat
	binary.LittleEndian.PutUint16(tmpl[105:107], spec.recordLen)

	// Point counts: legacy for <1.4, extended for 1.4+ (legacy zeroed).
	if spec.versionMinor >= 4 {
		binary.LittleEndian.PutUint64(tmpl[247:255], uint64(spec.numPoints))
	} else {
		binary.LittleEndian.PutUint32(tmpl[107:111], spec.numPoints)
	}

	// Scales 0.001, offsets 0, bbox 0.
	for _, off := range []int{131, 139, 147} {
		binary.LittleEndian.PutUint64(tmpl[off:off+8], math.Float64bits(0.001))
	}

	// EVLR fields (LAS 1.4+).
	if spec.versionMinor >= 4 && len(spec.evlrs) > 0 {
		evlrOffset := uint64(offsetToPoints) + uint64(len(spec.points))
		binary.LittleEndian.PutUint32(tmpl[235:239], uint32(len(spec.evlrs)))
		binary.LittleEndian.PutUint64(tmpl[239:247], evlrOffset)
	}

	hdr := make([]byte, hs)
	copy(hdr, tmpl) // truncates when hs < 375, zero-pads when hs > 375

	out := append(hdr, vlrBytes...)
	out = append(out, spec.points...)
	for _, e := range spec.evlrs {
		out = append(out, e...)
	}
	return out
}

// makeVLR builds raw VLR bytes: 54-byte header + payload.
// userID may contain embedded NULs and is written verbatim (up to 16 bytes).
func makeVLR(userID string, recID uint16, payload []byte) []byte {
	b := make([]byte, 54+len(payload))
	copy(b[2:18], userID)
	binary.LittleEndian.PutUint16(b[18:20], recID)
	binary.LittleEndian.PutUint16(b[20:22], uint16(len(payload)))
	copy(b[22:54], "test vlr")
	copy(b[54:], payload)
	return b
}

// makeEVLR builds raw EVLR bytes with an arbitrary declared record length
// (which may deliberately exceed the actual payload for hardening tests).
func makeEVLR(userID string, recID uint16, declaredLen uint64, payload []byte) []byte {
	b := make([]byte, 60+len(payload))
	copy(b[2:18], userID)
	binary.LittleEndian.PutUint16(b[18:20], recID)
	binary.LittleEndian.PutUint64(b[20:28], declaredLen)
	copy(b[28:60], "test evlr")
	copy(b[60:], payload)
	return b
}

// makeLASzipVLRPayload builds a LASzip VLR payload (34 + 6*n bytes).
func makeLASzipVLRPayload(compressor, coder uint16, chunkSize uint32, items [][3]uint16) []byte {
	b := make([]byte, 34+6*len(items))
	binary.LittleEndian.PutUint16(b[0:2], compressor)
	binary.LittleEndian.PutUint16(b[2:4], coder)
	b[4] = 3 // version major
	b[5] = 5 // version minor
	binary.LittleEndian.PutUint32(b[12:16], chunkSize)
	binary.LittleEndian.PutUint64(b[16:24], ^uint64(0)) // special EVLRs: -1
	binary.LittleEndian.PutUint64(b[24:32], ^uint64(0))
	binary.LittleEndian.PutUint16(b[32:34], uint16(len(items)))
	for i, it := range items {
		off := 34 + 6*i
		binary.LittleEndian.PutUint16(b[off:off+2], it[0])   // type
		binary.LittleEndian.PutUint16(b[off+2:off+4], it[1]) // size
		binary.LittleEndian.PutUint16(b[off+4:off+6], it[2]) // version
	}
	return b
}

// ---------------------------------------------------------------------------
// HasWKTCRS — Global Encoding bit 4 (0x10) per LAS 1.4 spec, not bit 2 (0x04)
// ---------------------------------------------------------------------------

func TestHasWKTCRS(t *testing.T) {
	tests := []struct {
		name      string
		minor     uint8
		globalEnc uint16
		want      bool
	}{
		{"1.4 WKT bit set", 4, 0x10, true},
		{"1.4 WKT+time bits", 4, 0x11, true},
		{"1.4 external waveform bit only", 4, 0x04, false},
		{"1.4 no bits", 4, 0x00, false},
		{"1.4 all other bits", 4, 0x0F, false},
		{"1.2 WKT bit meaningless", 2, 0x10, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pf, rl := uint8(6), uint16(30)
			if tc.minor < 4 {
				pf, rl = 0, 20
			}
			data := buildLAS(lasSpec{
				versionMinor: tc.minor,
				globalEnc:    tc.globalEnc,
				pointFormat:  pf,
				recordLen:    rl,
			})
			r, err := OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			if got := r.Header().HasWKTCRS(); got != tc.want {
				t.Errorf("HasWKTCRS() = %v, want %v (globalEnc=%#04x)", got, tc.want, tc.globalEnc)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Header size validation — C++ laszip_dll requires ≥227, ≥235 for 1.3,
// ≥375 for 1.4; golaz must error (not panic) on undersized headers.
// ---------------------------------------------------------------------------

func TestOpenHeaderSizeValidation(t *testing.T) {
	tests := []struct {
		name       string
		minor      uint8
		headerSize uint16
		wantErr    bool
	}{
		{"1.2 header 100 (panic window)", 2, 100, true},
		{"1.2 header 226", 2, 226, true},
		{"1.2 header 227 ok", 2, 227, false},
		{"1.3 header 227 too small", 3, 227, true},
		{"1.3 header 235 ok", 3, 235, false},
		{"1.4 header 300 too small", 4, 300, true},
		{"1.4 header 375 ok", 4, 375, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pf, rl := uint8(0), uint16(20)
			if tc.minor >= 4 {
				pf, rl = 6, 30
			}
			data := buildLAS(lasSpec{
				versionMinor: tc.minor,
				headerSize:   tc.headerSize,
				pointFormat:  pf,
				recordLen:    rl,
			})
			r, err := OpenReader(bytes.NewReader(data))
			if tc.wantErr {
				if err == nil {
					r.Close()
					t.Fatalf("Open succeeded, want error for header size %d (LAS 1.%d)", tc.headerSize, tc.minor)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			r.Close()
		})
	}
}

// ---------------------------------------------------------------------------
// Extra-byte descriptor no_data/min/max — per LAS 1.4 R15 §4.3 (Extra Bytes VLR) and
// lasattributer.hpp these are upcast to 8 bytes on disk:
// unsigned → uint64, signed → int64, float32/float64 → float64.
// ---------------------------------------------------------------------------

func TestExtraByteDescriptorNumericFields(t *testing.T) {
	// encode writes v into an 8-byte anytype slot per the upcast rule.
	putU64 := func(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
	putI64 := func(b []byte, v int64) { binary.LittleEndian.PutUint64(b, uint64(v)) }
	putF64 := func(b []byte, v float64) { binary.LittleEndian.PutUint64(b, math.Float64bits(v)) }

	tests := []struct {
		name     string
		dataType uint8
		encode   func(b []byte)
		want     float64
	}{
		{"float32 (type 9) stored as f64", 9, func(b []byte) { putF64(b, 1.5) }, 1.5},
		{"float32 (type 9) negative", 9, func(b []byte) { putF64(b, -273.15) }, -273.15},
		{"float64 (type 10)", 10, func(b []byte) { putF64(b, 2.5) }, 2.5},
		{"uint8 (type 1) upcast to u64", 1, func(b []byte) { putU64(b, 200) }, 200},
		{"int16 (type 4) negative upcast to i64", 4, func(b []byte) { putI64(b, -5) }, -5},
		{"uint32 (type 5) upcast to u64", 5, func(b []byte) { putU64(b, 4000000000) }, 4000000000},
		{"int64 (type 8) negative", 8, func(b []byte) { putI64(b, -1234567890123) }, -1234567890123},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			desc := make([]byte, 192)
			desc[2] = tc.dataType
			desc[3] = 0x07 // no_data | min | max
			copy(desc[4:36], "attr")
			tc.encode(desc[40:48]) // no_data
			tc.encode(desc[64:72]) // min
			tc.encode(desc[88:96]) // max

			v := VLR{UserID: "LASF_Spec", RecordID: 4, Data: desc}
			descs, err := v.ExtraByteDescriptors()
			if err != nil {
				t.Fatalf("ExtraByteDescriptors: %v", err)
			}
			d := descs[0]
			if d.NoData != tc.want {
				t.Errorf("NoData = %v, want %v", d.NoData, tc.want)
			}
			if d.Min != tc.want {
				t.Errorf("Min = %v, want %v", d.Min, tc.want)
			}
			if d.Max != tc.want {
				t.Errorf("Max = %v, want %v", d.Max, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Compressed bit set without a LASzip VLR — C++ laszip_dll errors; golaz
// must not silently decode compressed bytes as raw points.
// ---------------------------------------------------------------------------

func TestOpenCompressedBitRequiresLASzipVLR(t *testing.T) {
	tests := []struct {
		name    string
		pfByte  uint8
		wantErr bool
	}{
		{"bit 7 set, no VLR", 0x80, true},
		{"bit 6 set, no VLR (experimental)", 0x40, true},
		{"no compression bits", 0x00, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildLAS(lasSpec{
				versionMinor: 2,
				pointFormat:  tc.pfByte, // pf0 + bits
				recordLen:    20,
			})
			r, err := OpenReader(bytes.NewReader(data))
			if tc.wantErr {
				if err == nil {
					r.Close()
					t.Fatalf("Open succeeded, want error for pf byte %#02x without LASzip VLR", tc.pfByte)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			r.Close()
		})
	}
}

// ---------------------------------------------------------------------------
// VLR / EVLR allocation hardening — declared counts/lengths must be
// validated against the file layout before allocating.
// ---------------------------------------------------------------------------

func TestOpenVLRCountHardening(t *testing.T) {
	// numVLRs = 0xFFFFFFFF but zero bytes of VLR space before point data.
	data := buildLAS(lasSpec{
		versionMinor: 2,
		pointFormat:  0,
		recordLen:    20,
	})
	// Patch the VLR count directly (buildLAS wrote 0).
	binary.LittleEndian.PutUint32(data[100:104], 0xFFFFFFFF)
	r, err := OpenReader(bytes.NewReader(data))
	if err == nil {
		r.Close()
		t.Fatal("Open succeeded, want error for VLR count 0xFFFFFFFF with no VLR space")
	}
}

func TestEVLRDeclaredLengthHardening(t *testing.T) {
	tests := []struct {
		name        string
		declaredLen uint64
	}{
		{"declared length exceeds MaxInt", 1 << 63},
		{"declared length exceeds file size", 1 << 40},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildLAS(lasSpec{
				versionMinor: 4,
				pointFormat:  6,
				recordLen:    30,
				evlrs:        [][]byte{makeEVLR("LASF_Projection", 2112, tc.declaredLen, nil)},
			})
			r, err := OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			if _, err := r.EVLRs(); err == nil {
				t.Fatalf("EVLRs() succeeded, want error for declared length %d", tc.declaredLen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Point record length must cover the base size of the declared point format.
// C++ compressed path enforces this via LASzip::check(); an undersized record
// must produce an Open error, never a Scan panic.
// ---------------------------------------------------------------------------

func TestOpenShortRecordLength(t *testing.T) {
	tests := []struct {
		name      string
		minor     uint8
		pf        uint8
		recordLen uint16
		wantErr   bool
	}{
		{"pf6 record 20 < base 30", 4, 6, 20, true},
		{"pf6 record 30 ok", 4, 6, 30, false},
		{"pf0 record 10 < base 20", 2, 0, 10, true},
		{"pf0 record 20 ok", 2, 0, 20, false},
		{"pf1 record 27 < base 28", 2, 1, 27, true},
		{"pf0 record 28 with extra bytes ok", 2, 0, 28, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildLAS(lasSpec{
				versionMinor: tc.minor,
				pointFormat:  tc.pf,
				recordLen:    tc.recordLen,
				numPoints:    1,
				points:       make([]byte, tc.recordLen),
			})
			r, err := OpenReader(bytes.NewReader(data))
			if tc.wantErr {
				if err == nil {
					r.Close()
					t.Fatalf("Open succeeded, want error for pf%d record length %d", tc.pf, tc.recordLen)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			var p Point
			if err := r.Scan(&p); err != nil {
				t.Fatalf("Scan: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VLR string fields terminate at the first NUL (C++ strcmp semantics):
// a userID of "laszip encoded\x00<junk>" is still the LASzip VLR.
// ---------------------------------------------------------------------------

func TestVLRUserIDInteriorNul(t *testing.T) {
	payload := makeLASzipVLRPayload(2, 0, 50000, [][3]uint16{{6 /*POINT10*/, 20, 2}})
	vlr := makeVLR("laszip encoded\x00J", 22204, payload)
	data := buildLAS(lasSpec{
		versionMinor: 2,
		pointFormat:  0x80, // pf0 | compressed bit
		recordLen:    20,
		vlrs:         [][]byte{vlr},
	})
	r, err := OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if !r.Header().IsCompressed {
		t.Error("IsCompressed = false; LASzip VLR with interior-NUL userID was not recognized")
	}
	vlrs := r.VLRs()
	if len(vlrs) != 1 || vlrs[0].UserID != "laszip encoded" {
		t.Errorf("VLR userID = %q, want %q (truncate at first NUL)", vlrs[0].UserID, "laszip encoded")
	}
}

// ---------------------------------------------------------------------------
// LASzip VLR validation — C++ laszip_dll calls LASzip::check(record_length):
// unsupported compressor / item-size mismatch must fail at Open.
// ---------------------------------------------------------------------------

func TestLASzipVLRValidation(t *testing.T) {
	tests := []struct {
		name       string
		compressor uint16
		coder      uint16
		items      [][3]uint16
		recordLen  uint16
		wantErr    string
	}{
		{"compressor 7 unsupported", 7, 0, [][3]uint16{{6, 20, 2}}, 20, "compressor"},
		{"coder 1 unsupported", 2, 1, [][3]uint16{{6, 20, 2}}, 20, "coder"},
		{"item sum 20 vs record length 42", 2, 0, [][3]uint16{{6, 20, 2}}, 42, ""},
		{"valid pf0 v2", 2, 0, [][3]uint16{{6, 20, 2}}, 20, "OK"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := makeLASzipVLRPayload(tc.compressor, tc.coder, 50000, tc.items)
			data := buildLAS(lasSpec{
				versionMinor: 2,
				pointFormat:  0x80,
				recordLen:    tc.recordLen,
				vlrs:         [][]byte{makeVLR("laszip encoded", 22204, payload)},
			})
			r, err := OpenReader(bytes.NewReader(data))
			if tc.wantErr == "OK" {
				if err != nil {
					t.Fatalf("Open: %v", err)
				}
				r.Close()
				return
			}
			if err == nil {
				r.Close()
				t.Fatalf("Open succeeded, want error (%s)", tc.name)
			}
			if tc.wantErr != "" && !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}
