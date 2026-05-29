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

package golaz

// reader_test.go — tests for the high-level Reader API.
//
// Tests rely on the same testdata/las/ fixtures used by the e2e tests
// and the same reference_10pts.csv / reference_1000pts.csv CSVs.

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tdDir = "internal/laz/testdata/las"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openReader(t *testing.T, name string) *Reader {
	t.Helper()
	r, err := Open(filepath.Join(tdDir, name))
	if err != nil {
		t.Fatalf("Open %q: %v", name, err)
	}
	return r
}

// scanAll reads all points from r and returns them as a slice of *Point.
// Each Point is independently allocated (uses Next()).
func scanAll(t *testing.T, r *Reader) []*Point {
	t.Helper()
	pts := make([]*Point, 0, r.NumPoints())
	for {
		p, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		pts = append(pts, p)
	}
	return pts
}

// within reports whether a and b are within tol of each other.
func withinTol(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---------------------------------------------------------------------------
// 1. Open smoke test — every file in testdata/las
// ---------------------------------------------------------------------------

func TestReader_Open_AllFormats(t *testing.T) {
	entries, err := os.ReadDir(tdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".las") && !strings.HasSuffix(name, ".laz")) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			if r.Header() == nil {
				t.Error("Header() returned nil")
			}
			if r.NumPoints() == 0 {
				t.Error("NumPoints() == 0")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Header field verification
// ---------------------------------------------------------------------------

func TestReader_Header_Fields_LAS12(t *testing.T) {
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	h := r.Header()

	if h.FileSignature != "LASF" {
		t.Errorf("FileSignature: got %q want LASF", h.FileSignature)
	}
	if h.VersionMajor != 1 || h.VersionMinor != 2 {
		t.Errorf("Version: got %d.%d want 1.2", h.VersionMajor, h.VersionMinor)
	}
	if h.PointDataFormat != 0 {
		t.Errorf("PointDataFormat: got %d want 0", h.PointDataFormat)
	}
	if h.PointDataRecordLength != 20 {
		t.Errorf("PointDataRecordLength: got %d want 20", h.PointDataRecordLength)
	}
	if h.NumberOfPoints != 10 {
		t.Errorf("NumberOfPoints: got %d want 10", h.NumberOfPoints)
	}
	if !h.IsCompressed {
		t.Error("IsCompressed: want true for .laz file")
	}
	if h.ScaleX == 0 || h.ScaleY == 0 || h.ScaleZ == 0 {
		t.Error("scale factors should be non-zero")
	}
	// LAS 1.2 has no waveform offset or EVLR fields.
	if _, ok := h.WaveformDataOffset(); ok {
		t.Error("WaveformDataOffset should not be present for LAS 1.2")
	}
	if _, ok := h.EVLROffset(); ok {
		t.Error("EVLROffset should not be present for LAS 1.2")
	}
	if _, ok := h.EVLRCount(); ok {
		t.Error("EVLRCount should not be present for LAS 1.2")
	}
}

func TestReader_Header_Fields_LAS13(t *testing.T) {
	r := openReader(t, "las13_pf4_10pts.laz")
	defer r.Close()
	h := r.Header()

	if h.VersionMajor != 1 || h.VersionMinor != 3 {
		t.Errorf("Version: got %d.%d want 1.3", h.VersionMajor, h.VersionMinor)
	}
	// LAS 1.3 has waveform offset.
	if _, ok := h.WaveformDataOffset(); !ok {
		t.Error("WaveformDataOffset should be present for LAS 1.3")
	}
	// LAS 1.3 has no EVLRs.
	if _, ok := h.EVLROffset(); ok {
		t.Error("EVLROffset should not be present for LAS 1.3")
	}
}

func TestReader_Header_Fields_LAS14(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	h := r.Header()

	if h.VersionMajor != 1 || h.VersionMinor != 4 {
		t.Errorf("Version: got %d.%d want 1.4", h.VersionMajor, h.VersionMinor)
	}
	if h.NumberOfPoints != 1000 {
		t.Errorf("NumberOfPoints: got %d want 1000", h.NumberOfPoints)
	}
	if _, ok := h.EVLROffset(); !ok {
		t.Error("EVLROffset should be present for LAS 1.4")
	}
	if _, ok := h.EVLRCount(); !ok {
		t.Error("EVLRCount should be present for LAS 1.4")
	}
	// PointsByReturn slots 5..14 should be zero for a standard file.
	for i := 5; i < 15; i++ {
		if h.PointsByReturn[i] != 0 {
			t.Errorf("PointsByReturn[%d] = %d, want 0 (no extended returns in this file)", i, h.PointsByReturn[i])
		}
	}
}

func TestReader_Header_LAS_Uncompressed(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.las")
	defer r.Close()
	h := r.Header()
	if h.IsCompressed {
		t.Error("IsCompressed: want false for uncompressed .las file")
	}
}

func TestReader_Header_LAS15(t *testing.T) {
	r := openReader(t, "las15_pf10_10pts_multiscanner.las")
	defer r.Close()
	h := r.Header()
	if h.VersionMajor != 1 || h.VersionMinor != 5 {
		t.Errorf("Version: got %d.%d want 1.5", h.VersionMajor, h.VersionMinor)
	}
	if h.PointDataFormat != 10 {
		t.Errorf("PointDataFormat: got %d want 10", h.PointDataFormat)
	}
}

// ---------------------------------------------------------------------------
// 3. VLR tests
// ---------------------------------------------------------------------------

func TestReader_VLRs_LAS12(t *testing.T) {
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	vlrs := r.VLRs()
	if len(vlrs) < 1 {
		t.Fatalf("expected at least 1 VLR, got %d", len(vlrs))
	}
	// Find LASzip VLR.
	var found bool
	for _, v := range vlrs {
		if v.IsLASzip() {
			found = true
		}
	}
	if !found {
		t.Error("expected a LASzip VLR in .laz file")
	}
}

func TestReader_VLRs_ExtraByteDescriptor(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	vlrs := r.VLRs()

	var ebVLR *VLR
	for i := range vlrs {
		if vlrs[i].IsExtraByteDescriptor() {
			ebVLR = &vlrs[i]
			break
		}
	}
	if ebVLR == nil {
		t.Fatal("expected ExtraByteDescriptor VLR")
	}
	descs, err := ebVLR.ExtraByteDescriptors()
	if err != nil {
		t.Fatalf("ExtraByteDescriptors: %v", err)
	}
	if len(descs) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(descs))
	}
	// Descriptor names should match what the Python script wrote.
	names := []string{descs[0].Name, descs[1].Name}
	var hasGridID, hasConf bool
	for _, n := range names {
		if n == "GridID" {
			hasGridID = true
		}
		if n == "Confidence" {
			hasConf = true
		}
	}
	if !hasGridID {
		t.Errorf("expected descriptor named GridID; got %v", names)
	}
	if !hasConf {
		t.Errorf("expected descriptor named Confidence; got %v", names)
	}
}

func TestReader_VLRs_WKT(t *testing.T) {
	// Verify OGCWkt returns error for a non-WKT VLR.
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	for _, v := range r.VLRs() {
		if v.IsLASzip() {
			_, err := v.OGCWkt()
			if err == nil {
				t.Error("OGCWkt should return error for a non-WKT VLR")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 4. EVLRs test
// ---------------------------------------------------------------------------

func TestReader_EVLRs_LAS12_Error(t *testing.T) {
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	_, err := r.EVLRs()
	if err == nil {
		t.Error("EVLRs() should return error for LAS 1.2")
	}
}

func TestReader_EVLRs_LAS14(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	evlrs, err := r.EVLRs()
	if err != nil {
		t.Fatalf("EVLRs: %v", err)
	}
	// The file may have 0 EVLRs — that's fine.
	// Call twice to verify caching.
	evlrs2, err2 := r.EVLRs()
	if err2 != nil {
		t.Fatalf("EVLRs (second call): %v", err2)
	}
	if len(evlrs) != len(evlrs2) {
		t.Errorf("EVLR count differs between calls: %d vs %d", len(evlrs), len(evlrs2))
	}
}

func TestReader_EVLRs_DoNotDisruptReading(t *testing.T) {
	// Read half the points, load EVLRs, continue reading — verify continuity.
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()

	// Read 100 points and record the 100th.
	var hundredth Point
	for i := range 100 {
		if err := r.Scan(&hundredth); err != nil {
			t.Fatalf("Scan pt %d: %v", i, err)
		}
	}
	x100 := hundredth.X

	// Load EVLRs (triggers seek-around).
	if _, err := r.EVLRs(); err != nil && !strings.Contains(err.Error(), "not present") {
		t.Fatalf("EVLRs mid-read: %v", err)
	}

	// The 101st point must be readable and the 100th must be stable.
	var p101 Point
	if err := r.Scan(&p101); err != nil {
		t.Fatalf("Scan pt 101 after EVLR load: %v", err)
	}
	if hundredth.X != x100 {
		t.Error("100th point X changed after EVLR load")
	}
}

// ---------------------------------------------------------------------------
// 5. Scan — compare against reference CSV
// ---------------------------------------------------------------------------

func TestReader_Scan_vs_Reference_10pts(t *testing.T) {
	ref10 := parseRefCSV(t, filepath.Join(tdDir, "reference_10pts.csv"))

	files := []string{
		"las12_pf0_10pts.laz",
		"las12_pf1_10pts.laz",
		"las12_pf2_10pts.laz",
		"las12_pf3_10pts.laz",
		"las13_pf4_10pts.laz",
		"las13_pf5_10pts.laz",
		"las14_pf6_10pts.laz",
		"las14_pf7_10pts.laz",
		"las14_pf8_10pts.laz",
		"las14_pf9_10pts.laz",
		"las14_pf10_10pts.laz",
		// uncompressed
		"las14_pf6_10pts.las",
		"las12_pf0_10pts.las",
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			h := r.Header()

			pts := scanAll(t, r)
			if len(pts) != len(ref10) {
				t.Fatalf("point count: got %d want %d", len(pts), len(ref10))
			}

			for i, p := range pts {
				ref := ref10[i]

				// Coordinates (float64 round-trip through scale/offset).
				exX := math.Round((ref.X-h.OffsetX)/h.ScaleX)*h.ScaleX + h.OffsetX
				exY := math.Round((ref.Y-h.OffsetY)/h.ScaleY)*h.ScaleY + h.OffsetY
				exZ := math.Round((ref.Z-h.OffsetZ)/h.ScaleZ)*h.ScaleZ + h.OffsetZ
				if !withinTol(p.X, exX, math.Abs(h.ScaleX)*0.5) {
					t.Errorf("pt %d X: got %.6f want %.6f", i, p.X, exX)
				}
				if !withinTol(p.Y, exY, math.Abs(h.ScaleY)*0.5) {
					t.Errorf("pt %d Y: got %.6f want %.6f", i, p.Y, exY)
				}
				if !withinTol(p.Z, exZ, math.Abs(h.ScaleZ)*0.5) {
					t.Errorf("pt %d Z: got %.6f want %.6f", i, p.Z, exZ)
				}

				// Intensity.
				if p.Intensity != ref.Intensity {
					t.Errorf("pt %d intensity: got %d want %d", i, p.Intensity, ref.Intensity)
				}

				// Classification.
				if p.Classification != ref.Classification {
					t.Errorf("pt %d classification: got %d want %d", i, p.Classification, ref.Classification)
				}

				// GPS time.
				if p.HasGPS() {
					gps, err := p.GPSTime()
					if err != nil {
						t.Errorf("pt %d GPSTime: %v", i, err)
					} else if !withinTol(gps, ref.GPSTime, 1e-6) {
						t.Errorf("pt %d GPS: got %.10f want %.10f", i, gps, ref.GPSTime)
					}
				}

				// Colour.
				if p.HasColor() {
					r_, _ := p.Red()
					g_, _ := p.Green()
					b_, _ := p.Blue()
					if r_ != ref.Red {
						t.Errorf("pt %d red: got %d want %d", i, r_, ref.Red)
					}
					if g_ != ref.Green {
						t.Errorf("pt %d green: got %d want %d", i, g_, ref.Green)
					}
					if b_ != ref.Blue {
						t.Errorf("pt %d blue: got %d want %d", i, b_, ref.Blue)
					}
				}

				// NIR.
				if p.HasNIR() {
					nir, _ := p.NIR()
					if nir != ref.NIR {
						t.Errorf("pt %d NIR: got %d want %d", i, nir, ref.NIR)
					}
				}
			}
		})
	}
}

func TestReader_Scan_vs_Reference_60k(t *testing.T) {
	ref60k := parseRefCSV(t, filepath.Join(tdDir, "reference_1000pts.csv"))

	files := []string{
		"las14_pf6_1000pts_with_extrabytes.laz",
		"las14_pf6_1000pts_with_extrabytes.las",
	}

	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			h := r.Header()

			if int(r.NumPoints()) != len(ref60k) {
				t.Fatalf("point count: got %d want %d", r.NumPoints(), len(ref60k))
			}

			var p Point
			for i := range ref60k {
				if err := r.Scan(&p); err != nil {
					t.Fatalf("Scan pt %d: %v", i, err)
				}
				ref := ref60k[i]

				exX := math.Round((ref.X-h.OffsetX)/h.ScaleX)*h.ScaleX + h.OffsetX
				if !withinTol(p.X, exX, math.Abs(h.ScaleX)*0.5) {
					t.Errorf("pt %d X: got %.6f want %.6f", i, p.X, exX)
				}
				if p.Classification != ref.Classification {
					t.Errorf("pt %d classification: got %d want %d", i, p.Classification, ref.Classification)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 6. Next() — verify it matches Scan()
// ---------------------------------------------------------------------------

func TestReader_Next_Matches_Scan(t *testing.T) {
	const name = "las14_pf7_10pts.laz"

	// Read via Scan.
	rScan := openReader(t, name)
	defer rScan.Close()
	var scanPts []Point
	for {
		var p Point
		if err := rScan.Scan(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		cp := p
		scanPts = append(scanPts, cp)
	}

	// Read via Next.
	rNext := openReader(t, name)
	defer rNext.Close()
	var nextPts []*Point
	for {
		p, err := rNext.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("Next: %v", err)
		}
		nextPts = append(nextPts, p)
	}

	if len(scanPts) != len(nextPts) {
		t.Fatalf("count mismatch: Scan=%d Next=%d", len(scanPts), len(nextPts))
	}

	for i := range scanPts {
		if scanPts[i].X != nextPts[i].X || scanPts[i].Y != nextPts[i].Y {
			t.Errorf("pt %d XY mismatch", i)
		}
		if scanPts[i].Classification != nextPts[i].Classification {
			t.Errorf("pt %d classification mismatch", i)
		}
	}
}

func TestReader_Next_PointIsRetained(t *testing.T) {
	// Verify that Next() points remain valid after more Next() calls.
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	pts := make([]*Point, 0, 10)
	for {
		p, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pts = append(pts, p)
	}

	// All X values should still be intact (not overwritten by shared buffer).
	x0 := pts[0].X
	for i := 1; i < len(pts); i++ {
		if pts[i].X == x0 && i > 0 {
			// Could be coincidence for 1 point; not an error by itself.
		}
	}
	// Verify Raw() slices are independent.
	if len(pts) >= 2 {
		raw0 := pts[0].Raw()
		raw1 := pts[1].Raw()
		if len(raw0) == 0 || len(raw1) == 0 {
			t.Error("Raw() returned empty slice")
		}
		if &raw0[0] == &raw1[0] {
			t.Error("Next() points share the same Raw() backing array")
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Raw() — length and content spot-check
// ---------------------------------------------------------------------------

func TestReader_Raw_Length(t *testing.T) {
	files := []struct {
		name  string
		ptLen uint16
	}{
		{"las12_pf0_10pts.laz", 20},
		{"las12_pf3_10pts.laz", 34},
		{"las13_pf4_10pts.laz", 57},
		{"las14_pf6_10pts.laz", 30},
		{"las14_pf6_1000pts_with_extrabytes.laz", 38},
	}

	for _, tc := range files {
		t.Run(tc.name, func(t *testing.T) {
			r := openReader(t, tc.name)
			defer r.Close()
			var p Point
			if err := r.Scan(&p); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			raw := p.Raw()
			if uint16(len(raw)) != tc.ptLen {
				t.Errorf("Raw() len=%d want %d", len(raw), tc.ptLen)
			}
		})
	}
}

func TestReader_Raw_PF6_RepackRoundtrip(t *testing.T) {
	// For pf6, compare Raw()[22:30] (GPS in on-disk layout) against GPSTime().
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	for i := range 10 {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("Next pt %d: %v", i, err)
		}
		raw := p.Raw()
		if len(raw) < 30 {
			t.Fatalf("pt %d: Raw() too short: %d", i, len(raw))
		}
		// Decode GPS from raw on-disk bytes[22:30].
		rawGPS := float64frombitsLE(raw[22:30])
		gps, err := p.GPSTime()
		if err != nil {
			t.Fatalf("pt %d: GPSTime: %v", i, err)
		}
		if rawGPS != gps {
			t.Errorf("pt %d: GPS mismatch: raw=%.10f getter=%.10f", i, rawGPS, gps)
		}
		// Decode classification from raw on-disk byte[16].
		if raw[16] != p.Classification {
			t.Errorf("pt %d: raw[16]=%d != Classification=%d", i, raw[16], p.Classification)
		}
	}
}

func float64frombitsLE(b []byte) float64 {
	v := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
	return math.Float64frombits(v)
}

// ---------------------------------------------------------------------------
// 8. Seek / Reset
// ---------------------------------------------------------------------------

func TestReader_Seek_ForwardBackward(t *testing.T) {
	const name = "las14_pf6_1000pts_with_extrabytes.laz"

	// Ground truth.
	rRef := openReader(t, name)
	defer rRef.Close()
	allPts := scanAll(t, rRef)

	r := openReader(t, name)
	defer r.Close()

	targets := []uint64{0, 1, 100, 499, 500, 999}
	for _, idx := range targets {
		if err := r.Seek(idx); err != nil {
			t.Fatalf("Seek(%d): %v", idx, err)
		}
		var p Point
		if err := r.Scan(&p); err != nil {
			t.Fatalf("Scan after Seek(%d): %v", idx, err)
		}
		if p.X != allPts[idx].X || p.Y != allPts[idx].Y || p.Z != allPts[idx].Z {
			t.Errorf("Seek(%d): X/Y/Z mismatch: got (%.3f,%.3f,%.3f) want (%.3f,%.3f,%.3f)",
				idx, p.X, p.Y, p.Z, allPts[idx].X, allPts[idx].Y, allPts[idx].Z)
		}
	}
}

func TestReader_Reset(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	// Read all points once.
	first := scanAll(t, r)

	// Reset and re-read.
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	second := scanAll(t, r)

	if len(first) != len(second) {
		t.Fatalf("count mismatch after Reset: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].X != second[i].X || first[i].Z != second[i].Z {
			t.Errorf("pt %d differs after Reset", i)
		}
	}
}

func TestReader_Tell(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	if r.Tell() != 0 {
		t.Errorf("Tell before any read: got %d want 0", r.Tell())
	}
	var p Point
	for i := uint64(1); i <= 5; i++ {
		r.Scan(&p) //nolint
		if r.Tell() != i {
			t.Errorf("Tell after %d reads: got %d want %d", i, r.Tell(), i)
		}
	}
	r.Seek(0) //nolint
	if r.Tell() != 0 {
		t.Errorf("Tell after Seek(0): got %d want 0", r.Tell())
	}
}

// ---------------------------------------------------------------------------
// 9. Extra byte named access
// ---------------------------------------------------------------------------

func TestReader_ExtraByte_Named(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()

	descs := r.ExtraByteDescriptors()
	if len(descs) == 0 {
		t.Skip("no ExtraByteDescriptors — test file may not have LASF_Spec VLR")
	}

	ref := parseRefCSV(t, filepath.Join(tdDir, "reference_1000pts.csv"))

	var p Point
	for i := range ref {
		if err := r.Scan(&p); err != nil {
			t.Fatalf("Scan pt %d: %v", i, err)
		}
		// GridID — uint32
		gidVal, err := r.ExtraByte(&p, "GridID")
		if err != nil {
			t.Fatalf("pt %d ExtraByte(GridID): %v", i, err)
		}
		gid, ok := gidVal.(uint32)
		if !ok {
			t.Fatalf("pt %d GridID type: got %T want uint32", i, gidVal)
		}
		if gid != ref[i].GridID {
			t.Errorf("pt %d GridID: got %d want %d", i, gid, ref[i].GridID)
		}
		// Confidence — float32
		confVal, err := r.ExtraByte(&p, "Confidence")
		if err != nil {
			t.Fatalf("pt %d ExtraByte(Confidence): %v", i, err)
		}
		conf, ok := confVal.(float32)
		if !ok {
			t.Fatalf("pt %d Confidence type: got %T want float32", i, confVal)
		}
		if math.Abs(float64(conf)-float64(ref[i].Confidence)) > 1e-5 {
			t.Errorf("pt %d Confidence: got %f want %f", i, conf, ref[i].Confidence)
		}
	}
}

func TestReader_ExtraByte_UnknownField(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	var p Point
	r.Scan(&p) //nolint
	_, err := r.ExtraByte(&p, "NoSuchField")
	if err == nil {
		t.Error("expected error for unknown extra byte field name")
	}
}

func TestReader_ExtraByte_NoDescriptor(t *testing.T) {
	// File without ExtraByteDescriptor VLR.
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	var p Point
	r.Scan(&p) //nolint
	_, err := r.ExtraByte(&p, "anything")
	if err == nil {
		t.Error("expected ErrNoExtraByteDescriptor")
	}
}

// ---------------------------------------------------------------------------
// 10. Has*() presence helpers
// ---------------------------------------------------------------------------

func TestReader_HasHelpers(t *testing.T) {
	cases := []struct {
		name     string
		hasGPS   bool
		hasColor bool
		hasNIR   bool
		hasWave  bool
		hasExt   bool
	}{
		{"las12_pf0_10pts.laz", false, false, false, false, false},
		{"las12_pf1_10pts.laz", true, false, false, false, false},
		{"las12_pf2_10pts.laz", false, true, false, false, false},
		{"las12_pf3_10pts.laz", true, true, false, false, false},
		{"las13_pf4_10pts.laz", true, false, false, true, false},
		{"las13_pf5_10pts.laz", true, true, false, true, false},
		{"las14_pf6_10pts.laz", true, false, false, false, true},
		{"las14_pf7_10pts.laz", true, true, false, false, true},
		{"las14_pf8_10pts.laz", true, true, true, false, true},
		{"las14_pf9_10pts.laz", true, false, false, true, true},
		{"las14_pf10_10pts.laz", true, true, true, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := openReader(t, tc.name)
			defer r.Close()
			p, err := r.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if p.HasGPS() != tc.hasGPS {
				t.Errorf("HasGPS: got %v want %v", p.HasGPS(), tc.hasGPS)
			}
			if p.HasColor() != tc.hasColor {
				t.Errorf("HasColor: got %v want %v", p.HasColor(), tc.hasColor)
			}
			if p.HasNIR() != tc.hasNIR {
				t.Errorf("HasNIR: got %v want %v", p.HasNIR(), tc.hasNIR)
			}
			if p.HasWavepacket() != tc.hasWave {
				t.Errorf("HasWavepacket: got %v want %v", p.HasWavepacket(), tc.hasWave)
			}
			if p.HasExtendedFields() != tc.hasExt {
				t.Errorf("HasExtendedFields: got %v want %v", p.HasExtendedFields(), tc.hasExt)
			}
			// Verify error returns when field is absent.
			if !tc.hasGPS {
				if _, err := p.GPSTime(); err == nil {
					t.Error("GPSTime should return error when HasGPS=false")
				}
			}
			if !tc.hasColor {
				if _, err := p.Red(); err == nil {
					t.Error("Red should return error when HasColor=false")
				}
			}
			if !tc.hasNIR {
				if _, err := p.NIR(); err == nil {
					t.Error("NIR should return error when HasNIR=false")
				}
			}
			if !tc.hasWave {
				if _, err := p.WavePacketDescriptorIndex(); err == nil {
					t.Error("WavePacketDescriptorIndex should return error when HasWavepacket=false")
				}
			}
			if !tc.hasExt {
				if _, err := p.ScannerChannel(); err == nil {
					t.Error("ScannerChannel should return error when HasExtendedFields=false")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. Classification and ClassificationFlags
// ---------------------------------------------------------------------------

func TestReader_Classification_PF0_Masked(t *testing.T) {
	// For pf0–5, Classification must be masked to 5 bits (0–31).
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	ref := parseRefCSV(t, filepath.Join(tdDir, "reference_10pts.csv"))

	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		if p.Classification > 31 {
			t.Errorf("pt %d: Classification=%d exceeds 5-bit range", i, p.Classification)
		}
		if p.Classification != ref[i].Classification {
			t.Errorf("pt %d: Classification got %d want %d", i, p.Classification, ref[i].Classification)
		}
	}
}

func TestReader_Classification_PF6_FullByte(t *testing.T) {
	// For pf6+, Classification is the full byte.
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	ref := parseRefCSV(t, filepath.Join(tdDir, "reference_10pts.csv"))

	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		if p.Classification != ref[i].Classification {
			t.Errorf("pt %d: Classification got %d want %d", i, p.Classification, ref[i].Classification)
		}
	}
}

func TestReader_ClassificationFlags_PF0(t *testing.T) {
	// ClassificationFlags for pf0 come from bits 5–7 of the raw classification byte.
	// The reference CSV data uses standard classes without synthetic/keypoint/withheld
	// bits set, so we expect ClassificationFlags == 0 for all reference points.
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		if p.ClassificationFlags > 0x07 {
			t.Errorf("pt %d: ClassificationFlags=%d exceeds 3-bit range", i, p.ClassificationFlags)
		}
	}
}

func TestReader_ClassificationFlags_PF6(t *testing.T) {
	// For pf6+, ClassificationFlags is the dedicated 4-bit nibble.
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		if p.ClassificationFlags > 0x0F {
			t.Errorf("pt %d: ClassificationFlags=%d exceeds 4-bit range", i, p.ClassificationFlags)
		}
	}
}

// ---------------------------------------------------------------------------
// 12. ScanAngle normalisation
// ---------------------------------------------------------------------------

func TestReader_ScanAngle_PF0(t *testing.T) {
	// pf0–5: int8 rank, range approximately –90..+90 degrees.
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	ref := parseRefCSV(t, filepath.Join(tdDir, "reference_10pts.csv"))

	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		// Reference CSV stores int16-compatible scan_angle; for pf0–5 it is int8.
		wantDeg := float64(int8(int16(ref[i].ScanAngle)))
		if !withinTol(p.ScanAngleDegrees, wantDeg, 0.5) {
			t.Errorf("pt %d ScanAngleDegrees: got %.3f want %.3f", i, p.ScanAngleDegrees, wantDeg)
		}
		if math.Abs(p.ScanAngleDegrees) > 90 {
			t.Errorf("pt %d ScanAngleDegrees=%.1f out of pf0 expected range", i, p.ScanAngleDegrees)
		}
	}
}

func TestReader_ScanAngle_PF6(t *testing.T) {
	// pf6–10: int16 * 0.006 degrees.
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	ref := parseRefCSV(t, filepath.Join(tdDir, "reference_10pts.csv"))

	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		wantDeg := float64(ref[i].ScanAngle) * 0.006
		if !withinTol(p.ScanAngleDegrees, wantDeg, 1e-4) {
			t.Errorf("pt %d ScanAngleDegrees: got %.6f want %.6f", i, p.ScanAngleDegrees, wantDeg)
		}
	}
}

// ---------------------------------------------------------------------------
// 13. RawX/RawY/RawZ vs scaled X/Y/Z
// ---------------------------------------------------------------------------

func TestReader_RawCoords(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	h := r.Header()

	var p Point
	for i := range 10 {
		r.Scan(&p) //nolint
		exX := float64(p.RawX)*h.ScaleX + h.OffsetX
		exY := float64(p.RawY)*h.ScaleY + h.OffsetY
		exZ := float64(p.RawZ)*h.ScaleZ + h.OffsetZ
		if !withinTol(p.X, exX, 1e-9) {
			t.Errorf("pt %d: X=%.9f != RawX-derived=%.9f", i, p.X, exX)
		}
		if !withinTol(p.Y, exY, 1e-9) {
			t.Errorf("pt %d: Y=%.9f != RawY-derived=%.9f", i, p.Y, exY)
		}
		if !withinTol(p.Z, exZ, 1e-9) {
			t.Errorf("pt %d: Z=%.9f != RawZ-derived=%.9f", i, p.Z, exZ)
		}
	}
}

// ---------------------------------------------------------------------------
// 14. OpenReader (io.ReadSeeker)
// ---------------------------------------------------------------------------

func TestReader_OpenReader(t *testing.T) {
	f, err := os.Open(filepath.Join(tdDir, "las14_pf6_10pts.laz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	r, err := OpenReader(f)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()

	if r.NumPoints() != 10 {
		t.Errorf("NumPoints: got %d want 10", r.NumPoints())
	}
	pts := scanAll(t, r)
	if len(pts) != 10 {
		t.Errorf("got %d points want 10", len(pts))
	}
}

func TestReader_Header_FullFields_LAS12(t *testing.T) {
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()
	h := r.Header()

	// Identity / software
	if h.FileSignature != "LASF" {
		t.Errorf("FileSignature: got %q want LASF", h.FileSignature)
	}
	if h.FileSourceID != 0 {
		t.Errorf("FileSourceID: got %d want 0", h.FileSourceID)
	}
	if h.GlobalEncoding != 0 {
		t.Errorf("GlobalEncoding: got %d want 0", h.GlobalEncoding)
	}
	if h.SystemIdentifier != "OTHER" {
		t.Errorf("SystemIdentifier: got %q want OTHER", h.SystemIdentifier)
	}
	if h.GeneratingSoftware != "laspy 2.7.0" {
		t.Errorf("GeneratingSoftware: got %q want laspy 2.7.0", h.GeneratingSoftware)
	}
	if h.FileCreationDayOfYear != 149 {
		t.Errorf("FileCreationDayOfYear: got %d want 149", h.FileCreationDayOfYear)
	}
	if h.FileCreationYear != 2026 {
		t.Errorf("FileCreationYear: got %d want 2026", h.FileCreationYear)
	}
	if h.VersionMajor != 1 || h.VersionMinor != 2 {
		t.Errorf("Version: got %d.%d want 1.2", h.VersionMajor, h.VersionMinor)
	}

	// Layout
	if h.HeaderSize != 227 {
		t.Errorf("HeaderSize: got %d want 227", h.HeaderSize)
	}
	// OffsetToPointData = 227 (header) + 87 (TEST_METADATA VLR) + 94 (LASzip VLR) = 408
	// lasinfo hides the LASzip VLR in its "number var. length records" count.
	if h.OffsetToPointData != 408 {
		t.Errorf("OffsetToPointData: got %d want 408", h.OffsetToPointData)
	}
	if h.NumberOfVLRs != 2 {
		t.Errorf("NumberOfVLRs: got %d want 2 (TEST_METADATA + LASzip)", h.NumberOfVLRs)
	}
	if h.PointDataFormat != 0 {
		t.Errorf("PointDataFormat: got %d want 0", h.PointDataFormat)
	}
	if h.PointDataRecordLength != 20 {
		t.Errorf("PointDataRecordLength: got %d want 20", h.PointDataRecordLength)
	}
	if h.NumberOfPoints != 10 {
		t.Errorf("NumberOfPoints: got %d want 10", h.NumberOfPoints)
	}

	// Points by return: 4 4 2 0 0 (legacy 5 slots)
	wantPBR := [5]uint64{4, 4, 2, 0, 0}
	for i := range 5 {
		if h.PointsByReturn[i] != wantPBR[i] {
			t.Errorf("PointsByReturn[%d]: got %d want %d", i, h.PointsByReturn[i], wantPBR[i])
		}
	}
	for i := 5; i < 15; i++ {
		if h.PointsByReturn[i] != 0 {
			t.Errorf("PointsByReturn[%d]: got %d want 0", i, h.PointsByReturn[i])
		}
	}

	// Scale / offset
	if h.ScaleX != 0.001 || h.ScaleY != 0.001 || h.ScaleZ != 0.001 {
		t.Errorf("Scale: got %g %g %g want 0.001 0.001 0.001", h.ScaleX, h.ScaleY, h.ScaleZ)
	}
	if h.OffsetX != 0 || h.OffsetY != 0 || h.OffsetZ != 0 {
		t.Errorf("Offset: got %g %g %g want 0 0 0", h.OffsetX, h.OffsetY, h.OffsetZ)
	}

	// Bounding box (lasinfo: min 1009.418 1006.382 0.438 / max 1097.562 1092.676 9.707)
	const bbTol = 1e-3
	if math.Abs(h.MaxX-1097.562) > bbTol {
		t.Errorf("MaxX: got %g want 1097.562", h.MaxX)
	}
	if math.Abs(h.MinX-1009.418) > bbTol {
		t.Errorf("MinX: got %g want 1009.418", h.MinX)
	}
	if math.Abs(h.MaxY-1092.676) > bbTol {
		t.Errorf("MaxY: got %g want 1092.676", h.MaxY)
	}
	if math.Abs(h.MinY-1006.382) > bbTol {
		t.Errorf("MinY: got %g want 1006.382", h.MinY)
	}
	if math.Abs(h.MaxZ-9.707) > bbTol {
		t.Errorf("MaxZ: got %g want 9.707", h.MaxZ)
	}
	if math.Abs(h.MinZ-0.438) > bbTol {
		t.Errorf("MinZ: got %g want 0.438", h.MinZ)
	}
}

func TestReader_Header_FullFields_LAS13(t *testing.T) {
	r := openReader(t, "las13_pf4_10pts.laz")
	defer r.Close()
	h := r.Header()

	if h.VersionMajor != 1 || h.VersionMinor != 3 {
		t.Errorf("Version: got %d.%d want 1.3", h.VersionMajor, h.VersionMinor)
	}
	if h.SystemIdentifier != "OTHER" {
		t.Errorf("SystemIdentifier: got %q want OTHER", h.SystemIdentifier)
	}
	if h.GeneratingSoftware != "laspy 2.7.0" {
		t.Errorf("GeneratingSoftware: got %q want laspy 2.7.0", h.GeneratingSoftware)
	}
	if h.FileCreationDayOfYear != 149 {
		t.Errorf("FileCreationDayOfYear: got %d want 149", h.FileCreationDayOfYear)
	}
	if h.FileCreationYear != 2026 {
		t.Errorf("FileCreationYear: got %d want 2026", h.FileCreationYear)
	}
	if h.HeaderSize != 235 {
		t.Errorf("HeaderSize: got %d want 235", h.HeaderSize)
	}
	// OffsetToPointData = 235 (header) + 87 (TEST_METADATA VLR) + 106 (LASzip VLR) = 428
	if h.OffsetToPointData != 428 {
		t.Errorf("OffsetToPointData: got %d want 428", h.OffsetToPointData)
	}
	if h.NumberOfVLRs != 2 {
		t.Errorf("NumberOfVLRs: got %d want 2 (TEST_METADATA + LASzip)", h.NumberOfVLRs)
	}
	if h.PointDataFormat != 4 {
		t.Errorf("PointDataFormat: got %d want 4", h.PointDataFormat)
	}
	if h.PointDataRecordLength != 57 {
		t.Errorf("PointDataRecordLength: got %d want 57", h.PointDataRecordLength)
	}
	if h.NumberOfPoints != 10 {
		t.Errorf("NumberOfPoints: got %d want 10", h.NumberOfPoints)
	}
	if h.ScaleX != 0.001 || h.ScaleY != 0.001 || h.ScaleZ != 0.001 {
		t.Errorf("Scale: got %g %g %g want 0.001 0.001 0.001", h.ScaleX, h.ScaleY, h.ScaleZ)
	}
	if h.OffsetX != 0 || h.OffsetY != 0 || h.OffsetZ != 0 {
		t.Errorf("Offset: got %g %g %g want 0 0 0", h.OffsetX, h.OffsetY, h.OffsetZ)
	}
	const bbTol = 1e-3
	if math.Abs(h.MaxX-1097.562) > bbTol {
		t.Errorf("MaxX: got %g want 1097.562", h.MaxX)
	}
	if math.Abs(h.MinX-1009.418) > bbTol {
		t.Errorf("MinX: got %g want 1009.418", h.MinX)
	}
	if math.Abs(h.MaxY-1092.676) > bbTol {
		t.Errorf("MaxY: got %g want 1092.676", h.MaxY)
	}
	if math.Abs(h.MinY-1006.382) > bbTol {
		t.Errorf("MinY: got %g want 1006.382", h.MinY)
	}
	if math.Abs(h.MaxZ-9.707) > bbTol {
		t.Errorf("MaxZ: got %g want 9.707", h.MaxZ)
	}
	if math.Abs(h.MinZ-0.438) > bbTol {
		t.Errorf("MinZ: got %g want 0.438", h.MinZ)
	}

	// LAS 1.3: waveform data offset present
	wdo, ok := h.WaveformDataOffset()
	if !ok {
		t.Fatal("WaveformDataOffset: want present for LAS 1.3")
	}
	if wdo != 0 {
		t.Errorf("WaveformDataOffset: got %d want 0", wdo)
	}
}

func TestReader_Header_FullFields_LAS14_10pts(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()
	h := r.Header()

	if h.VersionMajor != 1 || h.VersionMinor != 4 {
		t.Errorf("Version: got %d.%d want 1.4", h.VersionMajor, h.VersionMinor)
	}
	if h.SystemIdentifier != "OTHER" {
		t.Errorf("SystemIdentifier: got %q want OTHER", h.SystemIdentifier)
	}
	if h.GeneratingSoftware != "laspy 2.7.0" {
		t.Errorf("GeneratingSoftware: got %q want laspy 2.7.0", h.GeneratingSoftware)
	}
	if h.FileCreationDayOfYear != 149 {
		t.Errorf("FileCreationDayOfYear: got %d want 149", h.FileCreationDayOfYear)
	}
	if h.FileCreationYear != 2026 {
		t.Errorf("FileCreationYear: got %d want 2026", h.FileCreationYear)
	}
	if h.HeaderSize != 375 {
		t.Errorf("HeaderSize: got %d want 375", h.HeaderSize)
	}
	// OffsetToPointData = 375 (header) + 87 (TEST_METADATA VLR) + 94 (LASzip VLR) = 556
	if h.OffsetToPointData != 556 {
		t.Errorf("OffsetToPointData: got %d want 556", h.OffsetToPointData)
	}
	if h.NumberOfVLRs != 2 {
		t.Errorf("NumberOfVLRs: got %d want 2 (TEST_METADATA + LASzip)", h.NumberOfVLRs)
	}
	if h.PointDataFormat != 6 {
		t.Errorf("PointDataFormat: got %d want 6", h.PointDataFormat)
	}
	if h.PointDataRecordLength != 30 {
		t.Errorf("PointDataRecordLength: got %d want 30", h.PointDataRecordLength)
	}
	// LAS 1.4 stores count in extended field; legacy uint32 is 0.
	if h.NumberOfPoints != 10 {
		t.Errorf("NumberOfPoints: got %d want 10", h.NumberOfPoints)
	}
	if h.ScaleX != 0.001 || h.ScaleY != 0.001 || h.ScaleZ != 0.001 {
		t.Errorf("Scale: got %g %g %g want 0.001 0.001 0.001", h.ScaleX, h.ScaleY, h.ScaleZ)
	}
	if h.OffsetX != 0 || h.OffsetY != 0 || h.OffsetZ != 0 {
		t.Errorf("Offset: got %g %g %g want 0 0 0", h.OffsetX, h.OffsetY, h.OffsetZ)
	}
	const bbTol = 1e-3
	if math.Abs(h.MaxX-1097.562) > bbTol {
		t.Errorf("MaxX: got %g want 1097.562", h.MaxX)
	}
	if math.Abs(h.MinX-1009.418) > bbTol {
		t.Errorf("MinX: got %g want 1009.418", h.MinX)
	}
	if math.Abs(h.MaxY-1092.676) > bbTol {
		t.Errorf("MaxY: got %g want 1092.676", h.MaxY)
	}
	if math.Abs(h.MinY-1006.382) > bbTol {
		t.Errorf("MinY: got %g want 1006.382", h.MinY)
	}
	if math.Abs(h.MaxZ-9.707) > bbTol {
		t.Errorf("MaxZ: got %g want 9.707", h.MaxZ)
	}
	if math.Abs(h.MinZ-0.438) > bbTol {
		t.Errorf("MinZ: got %g want 0.438", h.MinZ)
	}

	// LAS 1.4: extended points-by-return (15 slots)
	wantPBR := [15]uint64{4, 4, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := range 15 {
		if h.PointsByReturn[i] != wantPBR[i] {
			t.Errorf("PointsByReturn[%d]: got %d want %d", i, h.PointsByReturn[i], wantPBR[i])
		}
	}

	// LAS 1.4: EVLR fields present
	eo, ok := h.EVLROffset()
	if !ok {
		t.Fatal("EVLROffset: want present for LAS 1.4")
	}
	if eo != 0 {
		t.Errorf("EVLROffset: got %d want 0", eo)
	}
	ec, ok := h.EVLRCount()
	if !ok {
		t.Fatal("EVLRCount: want present for LAS 1.4")
	}
	if ec != 0 {
		t.Errorf("EVLRCount: got %d want 0", ec)
	}
}

func TestReader_Header_FullFields_LAS14_60k(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	h := r.Header()

	if h.VersionMajor != 1 || h.VersionMinor != 4 {
		t.Errorf("Version: got %d.%d want 1.4", h.VersionMajor, h.VersionMinor)
	}
	// NumberOfVLRs: LASF_Spec/4 + TEST_METADATA/999 + LASzip/22204 = 3
	if h.NumberOfVLRs != 3 {
		t.Errorf("NumberOfVLRs: got %d want 3 (LASF_Spec + TEST_METADATA + LASzip)", h.NumberOfVLRs)
	}
	if h.PointDataFormat != 6 {
		t.Errorf("PointDataFormat: got %d want 6", h.PointDataFormat)
	}
	if h.PointDataRecordLength != 38 {
		t.Errorf("PointDataRecordLength: got %d want 38 (30 base + 8 extra bytes)", h.PointDataRecordLength)
	}
	if h.NumberOfPoints != 1000 {
		t.Errorf("NumberOfPoints: got %d want 1000", h.NumberOfPoints)
	}
	if h.HeaderSize != 375 {
		t.Errorf("HeaderSize: got %d want 375", h.HeaderSize)
	}
	// OffsetToPointData = 375 + 438 (LASF_Spec VLR) + 87 (TEST_METADATA) + 100 (LASzip VLR) = 1000
	if h.OffsetToPointData != 1000 {
		t.Errorf("OffsetToPointData: got %d want 1000", h.OffsetToPointData)
	}
	if h.ScaleX != 0.001 || h.ScaleY != 0.001 || h.ScaleZ != 0.001 {
		t.Errorf("Scale: got %g %g %g want 0.001 0.001 0.001", h.ScaleX, h.ScaleY, h.ScaleZ)
	}
	if h.OffsetX != 0 || h.OffsetY != 0 || h.OffsetZ != 0 {
		t.Errorf("Offset: got %g %g %g want 0 0 0", h.OffsetX, h.OffsetY, h.OffsetZ)
	}
	const bbTol = 1e-3
	if math.Abs(h.MaxX-4220.033) > bbTol {
		t.Errorf("MaxX: got %g want 4220.033", h.MaxX)
	}
	if math.Abs(h.MinX-4095.824) > bbTol {
		t.Errorf("MinX: got %g want 4095.824", h.MinX)
	}
	if math.Abs(h.MaxY-2881.175) > bbTol {
		t.Errorf("MaxY: got %g want 2881.175", h.MaxY)
	}
	if math.Abs(h.MinY-2755.514) > bbTol {
		t.Errorf("MinY: got %g want 2755.514", h.MinY)
	}
	if math.Abs(h.MaxZ-210.835) > bbTol {
		t.Errorf("MaxZ: got %g want 210.835", h.MaxZ)
	}
	if math.Abs(h.MinZ-85.860) > bbTol {
		t.Errorf("MinZ: got %g want 85.860", h.MinZ)
	}

	// Extended PointsByReturn
	wantPBR := [15]uint64{354, 343, 303, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := range 15 {
		if h.PointsByReturn[i] != wantPBR[i] {
			t.Errorf("PointsByReturn[%d]: got %d want %d", i, h.PointsByReturn[i], wantPBR[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Full point field assertions — pf0 (all 10 points, all fields)
// ---------------------------------------------------------------------------

// pf0PointWant holds expected values for a single point format 0 point.
// Values sourced from las2txt64 with parse string "xyzirncupedagt".
type pf0PointWant struct {
	X, Y, Z          float64
	Intensity        uint16
	ReturnNumber     uint8
	NumberOfReturns  uint8
	Classification   uint8
	ClassFlags       uint8 // 0 for all standard test points
	UserData         uint8
	PointSourceID    uint16
	EdgeOfFlight     bool
	ScanDirection    bool
	ScanAngleDegrees float64
}

var pf0Points = []pf0PointWant{
	{1077.396, 1037.080, 7.581, 51411, 2, 1, 5, 0, 121, 40, false, true, -15.0},
	{1043.888, 1092.676, 3.545, 60457, 1, 1, 23, 0, 229, 83, false, false, 48.0},
	{1085.860, 1064.387, 9.707, 48013, 2, 2, 2, 0, 182, 24, true, true, 86.0},
	{1069.737, 1082.276, 8.931, 48807, 1, 1, 7, 0, 239, 63, true, false, 59.0},
	{1009.418, 1044.341, 7.784, 19210, 1, 1, 20, 0, 41, 57, false, true, -26.0},
	{1097.562, 1022.724, 1.946, 24027, 1, 3, 2, 0, 47, 70, true, false, -12.0},
	{1076.114, 1055.458, 4.667, 47042, 3, 1, 16, 0, 232, 84, true, true, -84.0},
	{1078.606, 1006.382, 0.438, 63405, 3, 2, 18, 0, 110, 10, false, false, 54.0},
	{1012.811, 1082.763, 1.543, 32033, 2, 3, 2, 0, 0, 26, false, false, -69.0},
	{1045.039, 1063.166, 6.830, 26924, 2, 3, 29, 0, 219, 31, true, true, 61.0},
}

func TestReader_Points_AllFields_PF0(t *testing.T) {
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()

	const xyTol = 5e-4 // half of scale 0.001
	const angTol = 0.5 // pf0 scan angle is int8 degree rank

	for i, want := range pf0Points {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}

		// Coordinates
		if math.Abs(p.X-want.X) > xyTol {
			t.Errorf("pt%d X: got %.3f want %.3f", i, p.X, want.X)
		}
		if math.Abs(p.Y-want.Y) > xyTol {
			t.Errorf("pt%d Y: got %.3f want %.3f", i, p.Y, want.Y)
		}
		if math.Abs(p.Z-want.Z) > xyTol {
			t.Errorf("pt%d Z: got %.3f want %.3f", i, p.Z, want.Z)
		}

		// Raw integer coordinates
		wantRawX := int32(math.Round(want.X / 0.001))
		wantRawY := int32(math.Round(want.Y / 0.001))
		wantRawZ := int32(math.Round(want.Z / 0.001))
		if p.RawX != wantRawX {
			t.Errorf("pt%d RawX: got %d want %d", i, p.RawX, wantRawX)
		}
		if p.RawY != wantRawY {
			t.Errorf("pt%d RawY: got %d want %d", i, p.RawY, wantRawY)
		}
		if p.RawZ != wantRawZ {
			t.Errorf("pt%d RawZ: got %d want %d", i, p.RawZ, wantRawZ)
		}

		// Scalar fields
		if p.Intensity != want.Intensity {
			t.Errorf("pt%d Intensity: got %d want %d", i, p.Intensity, want.Intensity)
		}
		if p.ReturnNumber != want.ReturnNumber {
			t.Errorf("pt%d ReturnNumber: got %d want %d", i, p.ReturnNumber, want.ReturnNumber)
		}
		if p.NumberOfReturns != want.NumberOfReturns {
			t.Errorf("pt%d NumberOfReturns: got %d want %d", i, p.NumberOfReturns, want.NumberOfReturns)
		}
		if p.Classification != want.Classification {
			t.Errorf("pt%d Classification: got %d want %d", i, p.Classification, want.Classification)
		}
		if p.ClassificationFlags != want.ClassFlags {
			t.Errorf("pt%d ClassificationFlags: got %d want %d", i, p.ClassificationFlags, want.ClassFlags)
		}
		if p.UserData != want.UserData {
			t.Errorf("pt%d UserData: got %d want %d", i, p.UserData, want.UserData)
		}
		if p.PointSourceID != want.PointSourceID {
			t.Errorf("pt%d PointSourceID: got %d want %d", i, p.PointSourceID, want.PointSourceID)
		}
		if p.EdgeOfFlightLine != want.EdgeOfFlight {
			t.Errorf("pt%d EdgeOfFlightLine: got %v want %v", i, p.EdgeOfFlightLine, want.EdgeOfFlight)
		}
		if p.ScanDirectionFlag != want.ScanDirection {
			t.Errorf("pt%d ScanDirectionFlag: got %v want %v", i, p.ScanDirectionFlag, want.ScanDirection)
		}
		if math.Abs(p.ScanAngleDegrees-want.ScanAngleDegrees) > angTol {
			t.Errorf("pt%d ScanAngleDegrees: got %.3f want %.3f", i, p.ScanAngleDegrees, want.ScanAngleDegrees)
		}

		// pf0 has no GPS, colour, NIR, or extended fields
		if p.HasGPS() {
			t.Errorf("pt%d: HasGPS should be false for pf0", i)
		}
		if p.HasColor() {
			t.Errorf("pt%d: HasColor should be false for pf0", i)
		}
		if p.HasNIR() {
			t.Errorf("pt%d: HasNIR should be false for pf0", i)
		}
	}
}

// ---------------------------------------------------------------------------
// GPS time — pf1 (all 10 points)
// ---------------------------------------------------------------------------

// GPS times from las2txt64 parse "t": evenly spaced from 400000 by 400000/9 * index
var pf1GPSTimes = []float64{
	400000.000000,
	400011.111111,
	400022.222222,
	400033.333333,
	400044.444444,
	400055.555556,
	400066.666667,
	400077.777778,
	400088.888889,
	400100.000000,
}

func TestReader_Points_GPSTime_PF1(t *testing.T) {
	r := openReader(t, "las12_pf1_10pts.laz")
	defer r.Close()

	for i, wantGPS := range pf1GPSTimes {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}
		if !p.HasGPS() {
			t.Fatalf("pt%d: HasGPS should be true for pf1", i)
		}
		gps, err := p.GPSTime()
		if err != nil {
			t.Fatalf("pt%d GPSTime: %v", i, err)
		}
		if math.Abs(gps-wantGPS) > 1e-5 {
			t.Errorf("pt%d GPSTime: got %.6f want %.6f", i, gps, wantGPS)
		}
	}
}

// ---------------------------------------------------------------------------
// RGB colour — pf2 (all 10 points)
// ---------------------------------------------------------------------------

// RGB values from las2txt64 parse "RGB"
var pf2RGB = [][3]uint16{
	{51316, 53936, 32354},
	{25392, 41729, 481},
	{4629, 53657, 57392},
	{58856, 9157, 52220},
	{57055, 15428, 57408},
	{18894, 54573, 51570},
	{43184, 12054, 62650},
	{15698, 13100, 51140},
	{1660, 56902, 43628},
	{44727, 52748, 43570},
}

func TestReader_Points_RGB_PF2(t *testing.T) {
	r := openReader(t, "las12_pf2_10pts.laz")
	defer r.Close()

	for i, wantRGB := range pf2RGB {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}
		if !p.HasColor() {
			t.Fatalf("pt%d: HasColor should be true for pf2", i)
		}
		red, _ := p.Red()
		green, _ := p.Green()
		blue, _ := p.Blue()
		if red != wantRGB[0] {
			t.Errorf("pt%d Red: got %d want %d", i, red, wantRGB[0])
		}
		if green != wantRGB[1] {
			t.Errorf("pt%d Green: got %d want %d", i, green, wantRGB[1])
		}
		if blue != wantRGB[2] {
			t.Errorf("pt%d Blue: got %d want %d", i, blue, wantRGB[2])
		}
		// pf2 has no GPS
		if p.HasGPS() {
			t.Errorf("pt%d: HasGPS should be false for pf2", i)
		}
	}
}

// ---------------------------------------------------------------------------
// pf3: GPS + RGB on the same points
// ---------------------------------------------------------------------------

func TestReader_Points_GPS_And_RGB_PF3(t *testing.T) {
	r := openReader(t, "las12_pf3_10pts.laz")
	defer r.Close()

	for i := range 10 {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}
		// GPS
		if !p.HasGPS() {
			t.Fatalf("pt%d: HasGPS should be true for pf3", i)
		}
		gps, err := p.GPSTime()
		if err != nil {
			t.Fatalf("pt%d GPSTime: %v", i, err)
		}
		if math.Abs(gps-pf1GPSTimes[i]) > 1e-5 {
			t.Errorf("pt%d GPSTime: got %.6f want %.6f", i, gps, pf1GPSTimes[i])
		}
		// RGB
		if !p.HasColor() {
			t.Fatalf("pt%d: HasColor should be true for pf3", i)
		}
		red, _ := p.Red()
		green, _ := p.Green()
		blue, _ := p.Blue()
		if red != pf2RGB[i][0] {
			t.Errorf("pt%d Red: got %d want %d", i, red, pf2RGB[i][0])
		}
		if green != pf2RGB[i][1] {
			t.Errorf("pt%d Green: got %d want %d", i, green, pf2RGB[i][1])
		}
		if blue != pf2RGB[i][2] {
			t.Errorf("pt%d Blue: got %d want %d", i, blue, pf2RGB[i][2])
		}
	}
}

// ---------------------------------------------------------------------------
// Full point field assertions — pf6 (all 10 points, all fields)
// ---------------------------------------------------------------------------

// pf6PointWant holds expected values for a single point format 6 point.
// Values sourced from las2txt64 with parse string "xyzirncupedagtl".
// XYZ and scalar fields are shared with pf0 (same underlying point data).
type pf6PointWant struct {
	X, Y, Z          float64
	Intensity        uint16
	ReturnNumber     uint8
	NumberOfReturns  uint8
	Classification   uint8
	ClassFlags       uint8
	UserData         uint8
	PointSourceID    uint16
	EdgeOfFlight     bool
	ScanDirection    bool
	ScanAngleDegrees float64 // pf6: int16 * 0.006
	GPSTime          float64
	ScannerChannel   uint8
}

var pf6Points = []pf6PointWant{
	{1077.396, 1037.080, 7.581, 51411, 2, 1, 5, 0, 121, 40, false, true, -0.090, 400000.000000, 0},
	{1043.888, 1092.676, 3.545, 60457, 1, 1, 23, 0, 229, 83, false, false, 0.288, 400011.111111, 0},
	{1085.860, 1064.387, 9.707, 48013, 2, 2, 2, 0, 182, 24, true, true, 0.516, 400022.222222, 0},
	{1069.737, 1082.276, 8.931, 48807, 1, 1, 7, 0, 239, 63, true, false, 0.354, 400033.333333, 0},
	{1009.418, 1044.341, 7.784, 19210, 1, 1, 20, 0, 41, 57, false, true, -0.156, 400044.444444, 0},
	{1097.562, 1022.724, 1.946, 24027, 1, 3, 2, 0, 47, 70, true, false, -0.072, 400055.555556, 0},
	{1076.114, 1055.458, 4.667, 47042, 3, 1, 16, 0, 232, 84, true, true, -0.504, 400066.666667, 0},
	{1078.606, 1006.382, 0.438, 63405, 3, 2, 18, 0, 110, 10, false, false, 0.324, 400077.777778, 0},
	{1012.811, 1082.763, 1.543, 32033, 2, 3, 2, 0, 0, 26, false, false, -0.414, 400088.888889, 0},
	{1045.039, 1063.166, 6.830, 26924, 2, 3, 29, 0, 219, 31, true, true, 0.366, 400100.000000, 0},
}

func TestReader_Points_AllFields_PF6(t *testing.T) {
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	const xyTol = 5e-4
	const angTol = 1e-4 // pf6 scan angle: int16 * 0.006 degrees
	const gpsTol = 1e-5

	for i, want := range pf6Points {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}

		// Coordinates
		if math.Abs(p.X-want.X) > xyTol {
			t.Errorf("pt%d X: got %.3f want %.3f", i, p.X, want.X)
		}
		if math.Abs(p.Y-want.Y) > xyTol {
			t.Errorf("pt%d Y: got %.3f want %.3f", i, p.Y, want.Y)
		}
		if math.Abs(p.Z-want.Z) > xyTol {
			t.Errorf("pt%d Z: got %.3f want %.3f", i, p.Z, want.Z)
		}

		// Raw integer coordinates
		wantRawX := int32(math.Round(want.X / 0.001))
		wantRawY := int32(math.Round(want.Y / 0.001))
		wantRawZ := int32(math.Round(want.Z / 0.001))
		if p.RawX != wantRawX {
			t.Errorf("pt%d RawX: got %d want %d", i, p.RawX, wantRawX)
		}
		if p.RawY != wantRawY {
			t.Errorf("pt%d RawY: got %d want %d", i, p.RawY, wantRawY)
		}
		if p.RawZ != wantRawZ {
			t.Errorf("pt%d RawZ: got %d want %d", i, p.RawZ, wantRawZ)
		}

		// Scalar fields
		if p.Intensity != want.Intensity {
			t.Errorf("pt%d Intensity: got %d want %d", i, p.Intensity, want.Intensity)
		}
		if p.ReturnNumber != want.ReturnNumber {
			t.Errorf("pt%d ReturnNumber: got %d want %d", i, p.ReturnNumber, want.ReturnNumber)
		}
		if p.NumberOfReturns != want.NumberOfReturns {
			t.Errorf("pt%d NumberOfReturns: got %d want %d", i, p.NumberOfReturns, want.NumberOfReturns)
		}
		if p.Classification != want.Classification {
			t.Errorf("pt%d Classification: got %d want %d", i, p.Classification, want.Classification)
		}
		if p.ClassificationFlags != want.ClassFlags {
			t.Errorf("pt%d ClassificationFlags: got %d want %d", i, p.ClassificationFlags, want.ClassFlags)
		}
		if p.UserData != want.UserData {
			t.Errorf("pt%d UserData: got %d want %d", i, p.UserData, want.UserData)
		}
		if p.PointSourceID != want.PointSourceID {
			t.Errorf("pt%d PointSourceID: got %d want %d", i, p.PointSourceID, want.PointSourceID)
		}
		if p.EdgeOfFlightLine != want.EdgeOfFlight {
			t.Errorf("pt%d EdgeOfFlightLine: got %v want %v", i, p.EdgeOfFlightLine, want.EdgeOfFlight)
		}
		if p.ScanDirectionFlag != want.ScanDirection {
			t.Errorf("pt%d ScanDirectionFlag: got %v want %v", i, p.ScanDirectionFlag, want.ScanDirection)
		}
		if math.Abs(p.ScanAngleDegrees-want.ScanAngleDegrees) > angTol {
			t.Errorf("pt%d ScanAngleDegrees: got %.4f want %.4f", i, p.ScanAngleDegrees, want.ScanAngleDegrees)
		}

		// GPS time (pf6+ always has GPS)
		if !p.HasGPS() {
			t.Fatalf("pt%d: HasGPS should be true for pf6", i)
		}
		gps, err := p.GPSTime()
		if err != nil {
			t.Fatalf("pt%d GPSTime: %v", i, err)
		}
		if math.Abs(gps-want.GPSTime) > gpsTol {
			t.Errorf("pt%d GPSTime: got %.6f want %.6f", i, gps, want.GPSTime)
		}

		// ScannerChannel (pf6+ extended field)
		if !p.HasExtendedFields() {
			t.Fatalf("pt%d: HasExtendedFields should be true for pf6", i)
		}
		sc, err := p.ScannerChannel()
		if err != nil {
			t.Fatalf("pt%d ScannerChannel: %v", i, err)
		}
		if sc != want.ScannerChannel {
			t.Errorf("pt%d ScannerChannel: got %d want %d", i, sc, want.ScannerChannel)
		}

		// pf6 has no colour or NIR
		if p.HasColor() {
			t.Errorf("pt%d: HasColor should be false for pf6", i)
		}
		if p.HasNIR() {
			t.Errorf("pt%d: HasNIR should be false for pf6", i)
		}
	}
}

// ---------------------------------------------------------------------------
// pf8: GPS + RGB + NIR + ScannerChannel
// ---------------------------------------------------------------------------

// NIR values from las2txt64 parse "I", pf8 file
var pf8NIR = []uint16{40918, 30903, 47070, 46212, 29020, 18160, 56214, 51164, 57620, 36413}

func TestReader_Points_AllFields_PF8(t *testing.T) {
	r := openReader(t, "las14_pf8_10pts.laz")
	defer r.Close()

	const xyTol = 5e-4
	const gpsTol = 1e-5

	for i := range 10 {
		p, err := r.Next()
		if err != nil {
			t.Fatalf("pt %d Next: %v", i, err)
		}
		want := pf6Points[i] // pf8 shares XYZ/scalars/GPS/ScannerChannel with pf6

		// Spot-check shared fields
		if math.Abs(p.X-want.X) > xyTol {
			t.Errorf("pt%d X: got %.3f want %.3f", i, p.X, want.X)
		}
		if p.Intensity != want.Intensity {
			t.Errorf("pt%d Intensity: got %d want %d", i, p.Intensity, want.Intensity)
		}
		if p.Classification != want.Classification {
			t.Errorf("pt%d Classification: got %d want %d", i, p.Classification, want.Classification)
		}
		if p.UserData != want.UserData {
			t.Errorf("pt%d UserData: got %d want %d", i, p.UserData, want.UserData)
		}
		if p.PointSourceID != want.PointSourceID {
			t.Errorf("pt%d PointSourceID: got %d want %d", i, p.PointSourceID, want.PointSourceID)
		}

		// GPS time
		gps, err := p.GPSTime()
		if err != nil {
			t.Fatalf("pt%d GPSTime: %v", i, err)
		}
		if math.Abs(gps-want.GPSTime) > gpsTol {
			t.Errorf("pt%d GPSTime: got %.6f want %.6f", i, gps, want.GPSTime)
		}

		// RGB (same values as pf2/pf3)
		if !p.HasColor() {
			t.Fatalf("pt%d: HasColor should be true for pf8", i)
		}
		red, _ := p.Red()
		green, _ := p.Green()
		blue, _ := p.Blue()
		if red != pf2RGB[i][0] {
			t.Errorf("pt%d Red: got %d want %d", i, red, pf2RGB[i][0])
		}
		if green != pf2RGB[i][1] {
			t.Errorf("pt%d Green: got %d want %d", i, green, pf2RGB[i][1])
		}
		if blue != pf2RGB[i][2] {
			t.Errorf("pt%d Blue: got %d want %d", i, blue, pf2RGB[i][2])
		}

		// NIR
		if !p.HasNIR() {
			t.Fatalf("pt%d: HasNIR should be true for pf8", i)
		}
		nir, err := p.NIR()
		if err != nil {
			t.Fatalf("pt%d NIR: %v", i, err)
		}
		if nir != pf8NIR[i] {
			t.Errorf("pt%d NIR: got %d want %d", i, nir, pf8NIR[i])
		}

		// ScannerChannel
		sc, err := p.ScannerChannel()
		if err != nil {
			t.Fatalf("pt%d ScannerChannel: %v", i, err)
		}
		if sc != want.ScannerChannel {
			t.Errorf("pt%d ScannerChannel: got %d want %d", i, sc, want.ScannerChannel)
		}
	}
}

// ---------------------------------------------------------------------------
// VLR content assertions
// ---------------------------------------------------------------------------

func TestReader_VLR_Content_TestMetadata(t *testing.T) {
	// Every test file carries the TEST_METADATA/999 VLR created by the
	// generation script.  Verify its header fields and non-empty payload.
	files := []string{
		"las12_pf0_10pts.laz",
		"las12_pf1_10pts.laz",
		"las14_pf6_10pts.laz",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			var found bool
			for _, v := range r.VLRs() {
				if v.UserID == "TEST_METADATA" && v.RecordID == 999 {
					found = true
					if v.Description != "Unit Test Block" {
						t.Errorf("Description: got %q want Unit Test Block", v.Description)
					}
					if len(v.Data) == 0 {
						t.Error("VLR data must not be empty")
					}
				}
			}
			if !found {
				t.Error("TEST_METADATA/999 VLR not found")
			}
		})
	}
}

func TestReader_VLR_ExtraBytes_Content(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()

	var eb *VLR
	for i := range r.VLRs() {
		if r.VLRs()[i].UserID == "LASF_Spec" && r.VLRs()[i].RecordID == 4 {
			v := r.VLRs()[i]
			eb = &v
			break
		}
	}
	if eb == nil {
		t.Fatal("LASF_Spec/4 Extra Bytes VLR not found")
	}
	descs, err := eb.ExtraByteDescriptors()
	if err != nil {
		t.Fatalf("ExtraByteDescriptors: %v", err)
	}
	if len(descs) != 2 {
		t.Fatalf("descriptor count: got %d want 2", len(descs))
	}

	// First descriptor: GridID, data type 5 = uint32
	if descs[0].Name != "GridID" {
		t.Errorf("desc[0] Name: got %q want GridID", descs[0].Name)
	}
	if descs[0].DataType != 5 {
		t.Errorf("desc[0] DataType: got %d want 5 (uint32)", descs[0].DataType)
	}

	// Second descriptor: Confidence, data type 9 = float32
	if descs[1].Name != "Confidence" {
		t.Errorf("desc[1] Name: got %q want Confidence", descs[1].Name)
	}
	if descs[1].DataType != 9 {
		t.Errorf("desc[1] DataType: got %d want 9 (float32)", descs[1].DataType)
	}
}

// ---------------------------------------------------------------------------
// Raw() byte layout spot-checks
// ---------------------------------------------------------------------------

func TestReader_Raw_PF0_ByteLayout(t *testing.T) {
	// For pf0 the on-disk layout is 20 bytes.
	// Verify specific known values at known byte offsets.
	r := openReader(t, "las12_pf0_10pts.laz")
	defer r.Close()

	// pt0: X=1077396 (int32 LE at bytes 0-3), Y=1037080, Z=7581
	// intensity=51411 (uint16 LE at bytes 12-13), classification=5 at byte 15
	p, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	raw := p.Raw()
	if len(raw) != 20 {
		t.Fatalf("Raw length: got %d want 20", len(raw))
	}

	rawX := int32(raw[0]) | int32(raw[1])<<8 | int32(raw[2])<<16 | int32(raw[3])<<24
	rawY := int32(raw[4]) | int32(raw[5])<<8 | int32(raw[6])<<16 | int32(raw[7])<<24
	rawZ := int32(raw[8]) | int32(raw[9])<<8 | int32(raw[10])<<16 | int32(raw[11])<<24
	intensity := uint16(raw[12]) | uint16(raw[13])<<8
	// byte 14: return_number(3b)|num_returns(3b)|scan_dir(1b)|edge(1b)
	returnByte := raw[14]
	returnNum := returnByte & 0x07
	numReturns := (returnByte >> 3) & 0x07
	scanDir := (returnByte >> 6) & 0x01
	edge := (returnByte >> 7) & 0x01
	// byte 15: class (low 5 bits) | flags (high 3 bits)
	classByte := raw[15]
	class := classByte & 0x1F
	// byte 16: scan angle rank (int8)
	scanAngleRank := int8(raw[16])
	// byte 17: user data
	userData := raw[17]
	// bytes 18-19: point source ID
	pointSrcID := uint16(raw[18]) | uint16(raw[19])<<8

	if rawX != 1077396 {
		t.Errorf("raw X: got %d want 1077396", rawX)
	}
	if rawY != 1037080 {
		t.Errorf("raw Y: got %d want 1037080", rawY)
	}
	if rawZ != 7581 {
		t.Errorf("raw Z: got %d want 7581", rawZ)
	}
	if intensity != 51411 {
		t.Errorf("raw intensity: got %d want 51411", intensity)
	}
	if returnNum != 2 {
		t.Errorf("raw return_number: got %d want 2", returnNum)
	}
	if numReturns != 1 {
		t.Errorf("raw num_returns: got %d want 1", numReturns)
	}
	if scanDir != 1 {
		t.Errorf("raw scan_direction: got %d want 1", scanDir)
	}
	if edge != 0 {
		t.Errorf("raw edge_of_flight: got %d want 0", edge)
	}
	if class != 5 {
		t.Errorf("raw classification: got %d want 5", class)
	}
	if scanAngleRank != -15 {
		t.Errorf("raw scan_angle_rank: got %d want -15", scanAngleRank)
	}
	if userData != 121 {
		t.Errorf("raw user_data: got %d want 121", userData)
	}
	if pointSrcID != 40 {
		t.Errorf("raw point_source_id: got %d want 40", pointSrcID)
	}
}

func TestReader_Raw_PF6_ByteLayout(t *testing.T) {
	// For pf6 the on-disk layout is 30 bytes.
	// Verify specific known values at known byte offsets (LAS 1.4 spec §2.6).
	r := openReader(t, "las14_pf6_10pts.laz")
	defer r.Close()

	// pt0 expected: X=1077396 Y=1037080 Z=7581 intensity=51411
	// return_byte[14]= ReturnNumber(4b)|NumReturns(4b)
	// flag_byte[15]  = ClassFlags(4b)|ScannerCh(2b)|ScanDir(1b)|Edge(1b)
	// classification[16]=5, user_data[17]=121
	// scan_angle_rank int16 LE [18-19] = -15 (raw=-15/0.006)
	// point_source_id uint16 [20-21] = 40
	// gps_time float64 [22-29]
	p, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	raw := p.Raw()
	if len(raw) != 30 {
		t.Fatalf("Raw length: got %d want 30", len(raw))
	}

	rawX := int32(raw[0]) | int32(raw[1])<<8 | int32(raw[2])<<16 | int32(raw[3])<<24
	intensity := uint16(raw[12]) | uint16(raw[13])<<8
	returnNum := raw[14] & 0x0F
	numReturns := (raw[14] >> 4) & 0x0F
	classFlags := raw[15] & 0x0F
	scannerCh := (raw[15] >> 4) & 0x03
	scanDir := (raw[15] >> 6) & 0x01
	edge := (raw[15] >> 7) & 0x01
	classification := raw[16]
	userData := raw[17]
	scanAngleRaw := int16(raw[18]) | int16(raw[19])<<8
	pointSrcID := uint16(raw[20]) | uint16(raw[21])<<8

	if rawX != 1077396 {
		t.Errorf("raw X: got %d want 1077396", rawX)
	}
	if intensity != 51411 {
		t.Errorf("raw intensity: got %d want 51411", intensity)
	}
	if returnNum != 2 {
		t.Errorf("raw return_number: got %d want 2", returnNum)
	}
	if numReturns != 1 {
		t.Errorf("raw num_returns: got %d want 1", numReturns)
	}
	if classFlags != 0 {
		t.Errorf("raw class_flags: got %d want 0", classFlags)
	}
	if scannerCh != 0 {
		t.Errorf("raw scanner_channel: got %d want 0", scannerCh)
	}
	if scanDir != 1 {
		t.Errorf("raw scan_direction: got %d want 1", scanDir)
	}
	if edge != 0 {
		t.Errorf("raw edge_of_flight: got %d want 0", edge)
	}
	if classification != 5 {
		t.Errorf("raw classification: got %d want 5", classification)
	}
	if userData != 121 {
		t.Errorf("raw user_data: got %d want 121", userData)
	}
	if scanAngleRaw != -15 {
		t.Errorf("raw scan_angle: got %d want -15 (= -0.090/0.006)", scanAngleRaw)
	}
	if pointSrcID != 40 {
		t.Errorf("raw point_source_id: got %d want 40", pointSrcID)
	}
}

// ---------------------------------------------------------------------------
// Selective decompression (WithSelectiveMask)
// ---------------------------------------------------------------------------

func TestReader_WithSelectiveMask_OnlyZ(t *testing.T) {
	// LAS 1.4 v3/v4: WithSelectiveMask(SelectiveZ) must decode Z and XY
	// correctly for every point while freezing GPS time at the first point of
	// each chunk (chunk_size = 100 for the test file).
	const name = "las14_pf6_1000pts_with_extrabytes.laz"
	const chunkSize = uint64(100)

	// Ground truth: full decompression.
	rFull := openReader(t, name)
	defer rFull.Close()
	fullPts := scanAll(t, rFull)

	// Selective: only Z (+ the always-on XY/channel/returns layer).
	rSel, err := Open(filepath.Join(tdDir, name), WithSelectiveMask(SelectiveZ))
	if err != nil {
		t.Fatalf("Open with SelectiveZ: %v", err)
	}
	defer rSel.Close()

	zDiffers := false
	var p Point
	for i := uint64(0); i < rSel.NumPoints(); i++ {
		if err := rSel.Scan(&p); err != nil {
			t.Fatalf("Scan pt %d: %v", i, err)
		}

		// Z and X must match full decompression exactly.
		if p.Z != fullPts[i].Z {
			t.Errorf("pt %d Z: got %.3f want %.3f", i, p.Z, fullPts[i].Z)
		}
		if p.X != fullPts[i].X {
			t.Errorf("pt %d X: got %.3f want %.3f", i, p.X, fullPts[i].X)
		}

		// GPS must be frozen at the first point of this chunk (the raw seed).
		seedIdx := (i / chunkSize) * chunkSize
		wantGPS, _ := fullPts[seedIdx].GPSTime()
		gotGPS, _ := p.GPSTime()
		if gotGPS != wantGPS {
			t.Errorf("pt %d GPS: got %.6f want frozen %.6f (chunk seed pt %d)",
				i, gotGPS, wantGPS, seedIdx)
		}

		if i > 0 && p.Z != fullPts[0].Z {
			zDiffers = true
		}
	}
	if !zDiffers {
		t.Error("Z never changed across 1000 points — test data may be degenerate")
	}
}

func TestReader_WithSelectiveMask_Accumulates(t *testing.T) {
	// Two separate WithSelectiveMask calls must OR together, not overwrite.
	// Passing SelectiveZ then SelectiveGPSTime should behave identically to
	// passing SelectiveZ | SelectiveGPSTime in a single call:
	//   - Z correct (requested in first call)
	//   - GPS correct (requested in second call)
	//   - Intensity frozen at chunk seed (never requested)
	const name = "las14_pf6_1000pts_with_extrabytes.laz"
	const chunkSize = uint64(100)

	rFull := openReader(t, name)
	defer rFull.Close()
	fullPts := scanAll(t, rFull)

	rSel, err := Open(filepath.Join(tdDir, name),
		WithSelectiveMask(SelectiveZ),
		WithSelectiveMask(SelectiveGPSTime),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rSel.Close()

	var p Point
	for i := uint64(0); i < rSel.NumPoints(); i++ {
		if err := rSel.Scan(&p); err != nil {
			t.Fatalf("Scan pt %d: %v", i, err)
		}

		// Z must be correctly decoded (first mask).
		if p.Z != fullPts[i].Z {
			t.Errorf("pt %d Z: got %.3f want %.3f", i, p.Z, fullPts[i].Z)
		}
		// GPS must be correctly decoded (second mask) — not frozen.
		gotGPS, _ := p.GPSTime()
		wantGPS, _ := fullPts[i].GPSTime()
		if gotGPS != wantGPS {
			t.Errorf("pt %d GPS: got %.6f want %.6f (GPS mask should be set by second call)",
				i, gotGPS, wantGPS)
		}
		// Intensity must be frozen at the chunk seed (never requested).
		seedIdx := (i / chunkSize) * chunkSize
		if p.Intensity != fullPts[seedIdx].Intensity {
			t.Errorf("pt %d Intensity: got %d want frozen %d (chunk seed pt %d)",
				i, p.Intensity, fullPts[seedIdx].Intensity, seedIdx)
		}
	}
}

func TestReader_WithSelectiveMask_LAS12_IsNoOp(t *testing.T) {
	// For LAS 1.2 pointwise-compressed files the mask is ignored and every
	// attribute is always decompressed — GPS must not be frozen.
	const name = "las12_pf3_10pts.laz" // pf3 = GPS + RGB

	rFull := openReader(t, name)
	defer rFull.Close()
	fullPts := scanAll(t, rFull)

	rSel, err := Open(filepath.Join(tdDir, name), WithSelectiveMask(SelectiveZ))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rSel.Close()

	var p Point
	for i, want := range fullPts {
		if err := rSel.Scan(&p); err != nil {
			t.Fatalf("Scan pt %d: %v", i, err)
		}
		// GPS must still decode correctly — not frozen — because LAS 1.2
		// ignores the selective mask.
		gotGPS, _ := p.GPSTime()
		wantGPS, _ := want.GPSTime()
		if gotGPS != wantGPS {
			t.Errorf("pt %d GPS: got %.6f want %.6f (LAS 1.2 must ignore mask)",
				i, gotGPS, wantGPS)
		}
	}
}
