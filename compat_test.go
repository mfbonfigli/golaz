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

// compat_test.go — table-driven tests for LAS 1.4 compatibility-mode reading
// (files written by `laszip -compatible`: pf6-10 content recoded as pf1/3/4/5
// plus "LAS 1.4 ..." extra-byte attributes and a lascompatible VLR).
// Expected behavior mirrors laszip_dll.cpp's laszip_open_reader /
// laszip_read_point reconstruction.
package golaz

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"testing"
)

// makeCompatVLRPayload builds the 2+2+4+148 byte lascompatible VLR payload.
func makeCompatVLRPayload(pointCount uint64, byReturn [15]uint64) []byte {
	b := make([]byte, 156)
	binary.LittleEndian.PutUint16(b[0:2], 3) // laszip version
	binary.LittleEndian.PutUint16(b[2:4], 3) // compatible version
	// u32 unused, u64 waveform start, u64 evlr start, u32 evlr count: zeros
	binary.LittleEndian.PutUint64(b[28:36], pointCount)
	for i, v := range byReturn {
		binary.LittleEndian.PutUint64(b[36+i*8:44+i*8], v)
	}
	return b
}

// makeExtraByteDescriptorPayload builds one 192-byte descriptor.
func makeExtraByteDescriptorPayload(dataType uint8, name string) []byte {
	d := make([]byte, 192)
	d[2] = dataType
	copy(d[4:36], name)
	return d
}

// compat attribute data types per laszip_dll.cpp write side:
// scan angle I16 (4), extended returns U8 (1), classification U8 (1),
// flags and channel U8 (1), NIR band U16 (3).
func compatDescriptors(withNIR bool, userFirst bool) []byte {
	var payload []byte
	if userFirst {
		payload = append(payload, makeExtraByteDescriptorPayload(5, "GridID")...) // uint32
	}
	payload = append(payload, makeExtraByteDescriptorPayload(4, "LAS 1.4 scan angle")...)
	payload = append(payload, makeExtraByteDescriptorPayload(1, "LAS 1.4 extended returns")...)
	payload = append(payload, makeExtraByteDescriptorPayload(1, "LAS 1.4 classification")...)
	payload = append(payload, makeExtraByteDescriptorPayload(1, "LAS 1.4 flags and channel")...)
	if withNIR {
		payload = append(payload, makeExtraByteDescriptorPayload(3, "LAS 1.4 NIR band")...)
	}
	return payload
}

// buildCompatPoint assembles one uncompressed point record for the synthetic
// compat files: pf1 (28 bytes) or pf3 (34 bytes), followed by
// [GridID u32] + scanAngleRem i16 + extReturns u8 + class u8 + flagsChan u8
// [+ NIR u16].
type compatPointSpec struct {
	rawX, rawY, rawZ int32
	intensity        uint16
	legacyRN         uint8 // 3-bit
	legacyNR         uint8 // 3-bit
	legacyClassByte  uint8 // 5-bit class | flag bits 5-7
	scanAngleRank    int8
	gps              float64
	rgb              [3]uint16 // pf3 only

	gridID       uint32
	scanAngleRem int16
	rnInc, nrInc uint8
	classAdd     uint8
	flagsChan    uint8
	nirBand      uint16
}

func buildCompatPointRecord(pf uint8, s compatPointSpec, withNIR bool) []byte {
	var b []byte
	le := binary.LittleEndian
	app16 := func(v uint16) { b = le.AppendUint16(b, v) }
	app32 := func(v uint32) { b = le.AppendUint32(b, v) }

	app32(uint32(s.rawX))
	app32(uint32(s.rawY))
	app32(uint32(s.rawZ))
	app16(s.intensity)
	b = append(b, s.legacyRN|s.legacyNR<<3)
	b = append(b, s.legacyClassByte)
	b = append(b, byte(s.scanAngleRank))
	b = append(b, 0) // user data
	app16(0)         // point source id
	b = le.AppendUint64(b, math.Float64bits(s.gps))
	if pf == 3 {
		app16(s.rgb[0])
		app16(s.rgb[1])
		app16(s.rgb[2])
	}
	// extra bytes: user attr first, then compat attrs
	app32(s.gridID)
	app16(uint16(s.scanAngleRem))
	b = append(b, s.rnInc<<4|s.nrInc)
	b = append(b, s.classAdd)
	b = append(b, s.flagsChan)
	if withNIR {
		app16(s.nirBand)
	}
	return b
}

func buildCompatLAS(t *testing.T, pf uint8, withNIR bool, s compatPointSpec) []byte {
	t.Helper()
	rec := buildCompatPointRecord(pf, s, withNIR)
	var byReturn [15]uint64
	byReturn[int(s.legacyRN+s.rnInc)-1] = 1
	return buildLAS(lasSpec{
		versionMinor: 2,
		pointFormat:  pf,
		recordLen:    uint16(len(rec)),
		numPoints:    1,
		vlrs: [][]byte{
			makeVLR("lascompatible", 22204, makeCompatVLRPayload(1, byReturn)),
			makeVLR("LASF_Spec", 4, compatDescriptors(withNIR, true)),
		},
		points: rec,
	})
}

func TestCompatibilityModeReconstruction(t *testing.T) {
	spec := compatPointSpec{
		rawX: 1000, rawY: 2000, rawZ: 3000,
		intensity:       500,
		legacyRN:        7,
		legacyNR:        7,
		legacyClassByte: 31 | 1<<5, // class 31 + synthetic flag
		scanAngleRank:   -15,
		gps:             400000.5,
		rgb:             [3]uint16{100, 200, 300},

		gridID:       123456,
		scanAngleRem: 3,
		rnInc:        8, // 7+8 = 15
		nrInc:        8,
		classAdd:     100,          // 31+100 = 131
		flagsChan:    (2 << 1) | 1, // scanner channel 2, overlap set
		nirBand:      777,
	}

	tests := []struct {
		name       string
		pf         uint8
		withNIR    bool
		wantPF     uint8
		wantRecLen uint16 // adjusted: pf1 37+2-5=34; pf3 45-3=42? see cases
	}{
		// pf1: 28 base + 9 extra = 37 on disk → pf6: +2-5 → 34 (30 base + 4 GridID)
		{"pf1 to pf6", 1, false, 6, 34},
		// pf3: 34 base + 9 extra = 43 → pf7: +2-5 → 40 (36 base + 4 GridID)
		{"pf3 to pf7", 3, false, 7, 40},
		// pf3+NIR: 34 base + 11 extra = 45 → pf8: +4-7 → 42 (38 base + 4 GridID)
		{"pf3 with NIR to pf8", 3, true, 8, 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := buildCompatLAS(t, tc.pf, tc.withNIR, spec)
			r, err := OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()

			h := r.Header()
			if h.PointDataFormat != tc.wantPF {
				t.Errorf("PointDataFormat = %d, want %d (reconstructed)", h.PointDataFormat, tc.wantPF)
			}
			if h.VersionMinor != 4 {
				t.Errorf("VersionMinor = %d, want 4 (upgraded)", h.VersionMinor)
			}
			if h.PointDataRecordLength != tc.wantRecLen {
				t.Errorf("PointDataRecordLength = %d, want %d", h.PointDataRecordLength, tc.wantRecLen)
			}
			// buildCompatLAS put the single point at return 15 → slot 14.
			if h.PointsByReturn[14] != 1 {
				t.Errorf("PointsByReturn not instilled from compat VLR: %v", h.PointsByReturn)
			}

			var p Point
			if err := r.Scan(&p); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if p.Format() != tc.wantPF {
				t.Errorf("Point.Format() = %d, want %d", p.Format(), tc.wantPF)
			}
			if !p.HasExtendedFields() {
				t.Error("HasExtendedFields() = false, want true")
			}
			if p.ReturnNumber != 15 {
				t.Errorf("ReturnNumber = %d, want 15 (7 legacy + 8 increment)", p.ReturnNumber)
			}
			if p.NumberOfReturns != 15 {
				t.Errorf("NumberOfReturns = %d, want 15", p.NumberOfReturns)
			}
			if p.Classification != 131 {
				t.Errorf("Classification = %d, want 131 (31 legacy + 100)", p.Classification)
			}
			// synthetic (bit0 from legacy) + overlap (bit3 from flags byte)
			if p.ClassificationFlags != 0b1001 {
				t.Errorf("ClassificationFlags = %#b, want 0b1001", p.ClassificationFlags)
			}
			ch, ok := p.ScannerChannel()
			if !ok || ch != 2 {
				t.Errorf("ScannerChannel = %d,%v, want 2,true", ch, ok)
			}
			// extended scan angle = remainder + I16_QUANTIZE(rank/0.006f)
			// rank -15 → -15/0.006 = -2500 → quantized -2500; +3 → -2497
			wantAngle := float64(-2497) * 0.006
			if math.Abs(p.ScanAngleDegrees-wantAngle) > 1e-9 {
				t.Errorf("ScanAngleDegrees = %v, want %v", p.ScanAngleDegrees, wantAngle)
			}
			if tc.withNIR {
				nir, ok := p.NIR()
				if !ok || nir != 777 {
					t.Errorf("NIR = %d,%v, want 777,true", nir, ok)
				}
			}
			if tc.pf == 3 {
				cr, cg, cb, ok := p.RGB()
				if !ok || cr != 100 || cg != 200 || cb != 300 {
					t.Errorf("RGB = %d,%d,%d,%v, want 100,200,300,true", cr, cg, cb, ok)
				}
			}
			gps, ok := p.GPSTime()
			if !ok || gps != spec.gps {
				t.Errorf("GPSTime = %v,%v, want %v,true", gps, ok, spec.gps)
			}

			// Only the user attribute remains visible.
			if got := len(p.ExtraBytes()); got != 4 {
				t.Errorf("visible ExtraBytes len = %d, want 4 (GridID only)", got)
			}
			v, err := r.ExtraByte(&p, "GridID")
			if err != nil {
				t.Fatalf("ExtraByte(GridID): %v", err)
			}
			if v.(uint32) != spec.gridID {
				t.Errorf("GridID = %v, want %d", v, spec.gridID)
			}
			if _, err := r.ExtraByte(&p, "LAS 1.4 scan angle"); !errors.Is(err, ErrUnknownExtraByteField) {
				t.Errorf("compat attribute still visible via ExtraByte: err=%v", err)
			}
		})
	}
}

func TestCompatibilityModeDisabled(t *testing.T) {
	spec := compatPointSpec{legacyRN: 1, legacyNR: 1, scanAngleRank: 5}
	data := buildCompatLAS(t, 1, false, spec)
	r, err := OpenReader(bytes.NewReader(data), WithCompatibilityMode(false))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if got := r.Header().PointDataFormat; got != 1 {
		t.Errorf("PointDataFormat = %d, want 1 (no reconstruction)", got)
	}
	var p Point
	if err := r.Scan(&p); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if p.Format() != 1 {
		t.Errorf("Point.Format() = %d, want 1", p.Format())
	}
	if got := len(p.ExtraBytes()); got != 9 {
		t.Errorf("ExtraBytes len = %d, want 9 (compat attributes exposed raw)", got)
	}
}

// ---------------------------------------------------------------------------
// C++ oracle fixtures: real compatibility-mode files written by laszip_dll
// (scripts/fixturegen Group E). The CSVs hold the values the C++ DLL reader
// reconstructs; the .las counterparts hold the same points written natively.
// ---------------------------------------------------------------------------

func within(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestCompatibilityModeCPPOracle(t *testing.T) {
	dir := filepath.Join("internal", "laz", "testdata", "cpporacle", "compat")
	tests := []struct {
		name       string
		base       string
		wantPF     uint8
		wantRecLen uint16
		hasRGB     bool
	}{
		{"pf6 compat", "las14_pf6_compat_1000pts", 6, 30, false},
		{"pf8 compat", "las14_pf8_compat_1000pts", 8, 38, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ref := parseRefCSV(t, filepath.Join(dir, tc.base+".csv"))

			laz, err := Open(filepath.Join(dir, tc.base+".laz"))
			if err != nil {
				t.Fatalf("Open laz: %v", err)
			}
			defer laz.Close()
			las, err := Open(filepath.Join(dir, tc.base+".las"))
			if err != nil {
				t.Fatalf("Open las: %v", err)
			}
			defer las.Close()

			// Header parity with what the C++ DLL reader reports.
			h := laz.Header()
			if h.PointDataFormat != tc.wantPF {
				t.Errorf("PointDataFormat = %d, want %d", h.PointDataFormat, tc.wantPF)
			}
			if h.PointDataRecordLength != tc.wantRecLen {
				t.Errorf("PointDataRecordLength = %d, want %d", h.PointDataRecordLength, tc.wantRecLen)
			}
			if h.VersionMinor != 4 {
				t.Errorf("VersionMinor = %d, want 4", h.VersionMinor)
			}
			if h.NumberOfPoints != uint64(len(ref)) {
				t.Fatalf("NumberOfPoints = %d, want %d", h.NumberOfPoints, len(ref))
			}

			var pz, pn Point
			for i, r := range ref {
				if err := laz.Scan(&pz); err != nil {
					t.Fatalf("laz Scan %d: %v", i, err)
				}
				if err := las.Scan(&pn); err != nil {
					t.Fatalf("las Scan %d: %v", i, err)
				}

				// 1. Reconstructed point vs the C++ DLL reconstruction CSV.
				if !within(pz.X, r.X, 1e-9) || !within(pz.Y, r.Y, 1e-9) || !within(pz.Z, r.Z, 1e-9) {
					t.Fatalf("pt %d coords (%v,%v,%v) != csv (%v,%v,%v)", i, pz.X, pz.Y, pz.Z, r.X, r.Y, r.Z)
				}
				if pz.Intensity != r.Intensity {
					t.Errorf("pt %d intensity %d != %d", i, pz.Intensity, r.Intensity)
				}
				if pz.ReturnNumber != r.ReturnNumber {
					t.Errorf("pt %d return number %d != %d", i, pz.ReturnNumber, r.ReturnNumber)
				}
				if pz.NumberOfReturns != r.NumberOfReturns {
					t.Errorf("pt %d number of returns %d != %d", i, pz.NumberOfReturns, r.NumberOfReturns)
				}
				if pz.Classification != r.Classification {
					t.Errorf("pt %d classification %d != %d", i, pz.Classification, r.Classification)
				}
				if pz.UserData != r.UserData {
					t.Errorf("pt %d user data %d != %d", i, pz.UserData, r.UserData)
				}
				if pz.PointSourceID != r.PointSourceID {
					t.Errorf("pt %d point source %d != %d", i, pz.PointSourceID, r.PointSourceID)
				}
				gotAngle := int16(math.Round(pz.ScanAngleDegrees / 0.006))
				if gotAngle != r.ScanAngle {
					t.Errorf("pt %d scan angle %d != %d", i, gotAngle, r.ScanAngle)
				}
				gps, _ := pz.GPSTime()
				if !within(gps, r.GPSTime, 1e-6) {
					t.Errorf("pt %d gps %v != %v", i, gps, r.GPSTime)
				}
				if tc.hasRGB {
					cr, cg, cb, ok := pz.RGB()
					if !ok || cr != r.Red || cg != r.Green || cb != r.Blue {
						t.Errorf("pt %d rgb %d,%d,%d,%v != %d,%d,%d", i, cr, cg, cb, ok, r.Red, r.Green, r.Blue)
					}
					nir, ok := pz.NIR()
					if !ok || nir != r.NIR {
						t.Errorf("pt %d nir %d,%v != %d", i, nir, ok, r.NIR)
					}
				}
				if got := len(pz.ExtraBytes()); got != 0 {
					t.Errorf("pt %d visible extra bytes = %d, want 0", i, got)
				}

				// 2. Full field parity against the native pf6/pf8 file,
				// covering fields the CSV lacks (scanner channel, flags).
				czl, _ := pz.ScannerChannel()
				czn, _ := pn.ScannerChannel()
				if czl != czn {
					t.Errorf("pt %d scanner channel %d != native %d", i, czl, czn)
				}
				if pz.ClassificationFlags != pn.ClassificationFlags {
					t.Errorf("pt %d classification flags %#b != native %#b", i, pz.ClassificationFlags, pn.ClassificationFlags)
				}
				if pz.ScanDirectionFlag != pn.ScanDirectionFlag || pz.EdgeOfFlightLine != pn.EdgeOfFlightLine {
					t.Errorf("pt %d scan dir/eofl mismatch vs native", i)
				}
				if pz.Format() != pn.Format() {
					t.Errorf("pt %d format %d != native %d", i, pz.Format(), pn.Format())
				}
			}
		})
	}
}

// A file with the marker VLR but missing required attributes must be read
// as a plain legacy file (C++ requires all four non-NIR attributes).
func TestCompatibilityModeMissingAttributes(t *testing.T) {
	rec := buildCompatPointRecord(1, compatPointSpec{legacyRN: 1, legacyNR: 1}, false)
	data := buildLAS(lasSpec{
		versionMinor: 2,
		pointFormat:  1,
		recordLen:    uint16(len(rec)),
		numPoints:    1,
		vlrs: [][]byte{
			makeVLR("lascompatible", 22204, makeCompatVLRPayload(1, [15]uint64{})),
			// descriptor VLR present but without the "LAS 1.4 ..." attributes
			makeVLR("LASF_Spec", 4, makeExtraByteDescriptorPayload(5, "GridID")),
		},
		points: rec,
	})
	r, err := OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if got := r.Header().PointDataFormat; got != 1 {
		t.Errorf("PointDataFormat = %d, want 1 (compat requires all four attributes)", got)
	}
}
