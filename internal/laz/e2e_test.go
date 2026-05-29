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

// Package laz provides LAZ (LASzip) decompression for LAS point cloud data.
//
// e2e_test.go — end-to-end tests:
//  1. Read every .las file, compare all point fields against reference CSV.
//  2. Read every .laz file, compare byte-for-byte against its .las counterpart.
package laz

import (
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Reference record (26 columns, no scan_angle_rank)
// ---------------------------------------------------------------------------

type refRecord struct {
	X, Y, Z          float64
	Intensity        uint16
	ReturnNumber     uint8
	NumberOfReturns  uint8
	ScanDirection    uint8
	EOFL             uint8
	Classification   uint8
	UserData         uint8
	PointSourceID    uint16
	ScanAngle        int16
	GPSTime          float64
	Red, Green, Blue uint16
	NIR              uint16
	WaveIdx          uint8
	WaveOff          uint64
	WaveSize         uint32
	WaveLoc          float32
	XT, YT, ZT       float64
	GridID           uint32
	Confidence       float32
}

// parseRefCSV reads a 26‑column reference CSV.
func parseRefCSV(t *testing.T, path string) []refRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ref csv %q: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read ref csv %q: %v", path, err)
	}
	if len(rows) < 2 {
		t.Fatalf("ref csv %q: no data rows", path)
	}

	recs := make([]refRecord, len(rows)-1)
	for i := 1; i < len(rows); i++ {
		f := rows[i]
		if len(f) < 26 {
			t.Fatalf("ref csv %q row %d: need 26 cols, got %d", path, i, len(f))
		}
		rr := &recs[i-1]
		rr.X = pFloat(f[0])
		rr.Y = pFloat(f[1])
		rr.Z = pFloat(f[2])
		rr.Intensity = uint16(pInt(f[3]))
		rr.ReturnNumber = uint8(pInt(f[4]))
		rr.NumberOfReturns = uint8(pInt(f[5]))
		rr.ScanDirection = uint8(pInt(f[6]))
		rr.EOFL = uint8(pInt(f[7]))
		rr.Classification = uint8(pInt(f[8]))
		rr.UserData = uint8(pInt(f[9]))
		rr.PointSourceID = uint16(pInt(f[10]))
		rr.ScanAngle = int16(pInt(f[11]))
		rr.GPSTime = pFloat(f[12])
		rr.Red = uint16(pInt(f[13]))
		rr.Green = uint16(pInt(f[14]))
		rr.Blue = uint16(pInt(f[15]))
		rr.NIR = uint16(pInt(f[16]))
		rr.WaveIdx = uint8(pInt(f[17]))
		rr.WaveOff = uint64(pInt(f[18]))
		rr.WaveSize = uint32(pInt(f[19]))
		rr.WaveLoc = float32(pFloat(f[20]))
		rr.XT = pFloat(f[21])
		rr.YT = pFloat(f[22])
		rr.ZT = pFloat(f[23])
		rr.GridID = uint32(pInt(f[24]))
		rr.Confidence = float32(pFloat(f[25]))
	}
	return recs
}

func pFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func pInt(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func within(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---------------------------------------------------------------------------
// POINT14 in‑memory helpers (40‑byte LAStempReadPoint10 layout)
// ---------------------------------------------------------------------------

func p14ScanAngle(buf []byte) int16      { return int16(buf[20]) | int16(buf[21])<<8 }
func p14UserData(buf []byte) uint8       { return buf[17] }
func p14PointSourceID(buf []byte) uint16 { return uint16(buf[18]) | uint16(buf[19])<<8 }
func p14ScanDir(buf []byte) uint8        { return (buf[14] >> 6) & 1 }
func p14EOFL(buf []byte) uint8           { return (buf[14] >> 7) & 1 }
func p14Class(buf []byte) uint8          { return buf[23] }
func p14GPS(buf []byte) float64 {
	return math.Float64frombits(
		uint64(buf[32]) | uint64(buf[33])<<8 | uint64(buf[34])<<16 | uint64(buf[35])<<24 |
			uint64(buf[36])<<32 | uint64(buf[37])<<40 | uint64(buf[38])<<48 | uint64(buf[39])<<56,
	)
}

// ---------------------------------------------------------------------------
// POINT10 helpers
// ---------------------------------------------------------------------------

func p10UserData(buf []byte) uint8       { return buf[17] }
func p10PointSourceID(buf []byte) uint16 { return uint16(buf[18]) | uint16(buf[19])<<8 }
func p10ScanDir(buf []byte) uint8        { return (buf[14] >> 6) & 1 }
func p10EOFL(buf []byte) uint8           { return (buf[14] >> 7) & 1 }

func nirVal(buf []byte, items []LASitem, offsets []uint32) uint16 {
	for i := range items {
		if items[i].Type == LASITEM_RGBNIR14 {
			o := offsets[i]
			return uint16(buf[o+6]) | uint16(buf[o+7])<<8
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// read all points from a file
// ---------------------------------------------------------------------------

type ptBuf struct {
	buf     []byte
	items   []LASitem
	offsets []uint32
}

func readAll(t *testing.T, path string) ([]ptBuf, *LASunzipper) {
	t.Helper()
	u, err := OpenLAS(path)
	if err != nil {
		t.Fatalf("OpenLAS %q: %v", path, err)
	}
	items := u.Items()
	offsets := u.Offsets()
	n := u.NumPoints()

	pts := make([]ptBuf, n)
	for i := range n {
		buf := make([]byte, offsets[len(offsets)-1])
		pt := make([][]byte, len(items))
		for j := range items {
			pt[j] = buf[offsets[j]:offsets[j+1]]
		}
		if err := u.Read(pt); err != nil {
			t.Fatalf("read pt %d from %q: %v", i, path, err)
		}
		pts[i] = ptBuf{buf: buf, items: items, offsets: offsets}
	}
	return pts, u
}

// ---------------------------------------------------------------------------
// Test 1: .las files vs reference CSV
// ---------------------------------------------------------------------------

func TestE2E_LAS_vs_Ref(t *testing.T) {
	td := filepath.Join("testdata", "las")

	var ref10, ref60k []refRecord
	var loaded bool
	load := func() {
		if loaded {
			return
		}
		ref10 = parseRefCSV(t, filepath.Join(td, "reference_10pts.csv"))
		ref60k = parseRefCSV(t, filepath.Join(td, "reference_1000pts.csv"))
		loaded = true
	}

	entries, err := os.ReadDir(td)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".las") || e.IsDir() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			load()

			var ref []refRecord
			switch {
			case strings.Contains(name, "_10pts"):
				ref = ref10
			case strings.Contains(name, "_1000pts"):
				ref = ref60k
			default:
				t.Skipf("unknown pattern: %s", name)
				return
			}

			pts, u := readAll(t, filepath.Join(td, name))
			defer u.Close()

			if len(pts) != len(ref) {
				t.Fatalf("count: file=%d ref=%d", len(pts), len(ref))
			}

			sx, sy, sz, ox, oy, oz := u.Scale()
			is14 := len(pts[0].items) > 0 && pts[0].items[0].Type == LASITEM_POINT14
			hasGPS, hasRGB, hasNIR := u.HasGPS(), u.HasRGB(), u.HasNIR()

			for pi, p := range pts {
				r := ref[pi]
				b := p.buf

				// ----- coordinates: raw int32 exact match -----
				rx, ry, rz := GetX(b), GetY(b), GetZ(b)
				exX := int32(math.Round((r.X - ox) / sx))
				exY := int32(math.Round((r.Y - oy) / sy))
				exZ := int32(math.Round((r.Z - oz) / sz))
				if rx != exX {
					t.Errorf("pt %d X: got %d want %d", pi, rx, exX)
				}
				if ry != exY {
					t.Errorf("pt %d Y: got %d want %d", pi, ry, exY)
				}
				if rz != exZ {
					t.Errorf("pt %d Z: got %d want %d", pi, rz, exZ)
				}

				// ----- intensity -----
				if gi := GetIntensity(b); gi != r.Intensity {
					t.Errorf("pt %d intensity: got %d want %d", pi, gi, r.Intensity)
				}

				// ----- return nr -----
				if grn := GetReturnNumber(b); grn != r.ReturnNumber {
					t.Errorf("pt %d rn: got %d want %d", pi, grn, r.ReturnNumber)
				}
				if gnr := GetNumberOfReturns(b); gnr != r.NumberOfReturns {
					t.Errorf("pt %d nr: got %d want %d", pi, gnr, r.NumberOfReturns)
				}

				// ----- classification -----
				if is14 {
					if gc := p14Class(b); gc != r.Classification {
						t.Errorf("pt %d class: got %d want %d", pi, gc, r.Classification)
					}
				} else {
					if gc := GetClassification(b, p.items); gc != r.Classification {
						t.Errorf("pt %d class: got %d want %d", pi, gc, r.Classification)
					}
				}

				if is14 {
					// ----- POINT14 extended fields -----
					if g := p14ScanDir(b); g != r.ScanDirection {
						t.Errorf("pt %d scan_dir: %d != %d", pi, g, r.ScanDirection)
					}
					if g := p14EOFL(b); g != r.EOFL {
						t.Errorf("pt %d eofl: %d != %d", pi, g, r.EOFL)
					}
					if g := p14UserData(b); g != r.UserData {
						t.Errorf("pt %d user_data: %d != %d", pi, g, r.UserData)
					}
					if g := p14PointSourceID(b); g != r.PointSourceID {
						t.Errorf("pt %d psrc: %d != %d", pi, g, r.PointSourceID)
					}
					if g := p14ScanAngle(b); g != r.ScanAngle {
						t.Errorf("pt %d scan_angle: %d != %d", pi, g, r.ScanAngle)
					}
					if g := p14GPS(b); !within(g, r.GPSTime, 1e-6) {
						t.Errorf("pt %d gps: %.10f != %.10f", pi, g, r.GPSTime)
					}
				} else {
					// ----- POINT10 extra fields -----
					if g := p10UserData(b); g != r.UserData {
						t.Errorf("pt %d user_data: %d != %d", pi, g, r.UserData)
					}
					if g := p10PointSourceID(b); g != r.PointSourceID {
						t.Errorf("pt %d psrc: %d != %d", pi, g, r.PointSourceID)
					}
					if g := p10ScanDir(b); g != r.ScanDirection {
						t.Errorf("pt %d scan_dir: %d != %d", pi, g, r.ScanDirection)
					}
					if g := p10EOFL(b); g != r.EOFL {
						t.Errorf("pt %d eofl: %d != %d", pi, g, r.EOFL)
					}
				}

				// ----- GPS (GPSTIME11 item) -----
				if hasGPS && !is14 {
					if g := GetGPS(b, p.items, p.offsets); !within(g, r.GPSTime, 1e-6) {
						t.Errorf("pt %d gps11: %.10f != %.10f", pi, g, r.GPSTime)
					}
				}

				// ----- RGB -----
				if hasRGB {
					gr, gg, gb := GetRGB(b, p.items, p.offsets)
					if gr != r.Red {
						t.Errorf("pt %d R: %d != %d", pi, gr, r.Red)
					}
					if gg != r.Green {
						t.Errorf("pt %d G: %d != %d", pi, gg, r.Green)
					}
					if gb != r.Blue {
						t.Errorf("pt %d B: %d != %d", pi, gb, r.Blue)
					}
				}

				// ----- NIR -----
				if hasNIR {
					if gn := nirVal(b, p.items, p.offsets); gn != r.NIR {
						t.Errorf("pt %d NIR: %d != %d", pi, gn, r.NIR)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 2: .laz files byte‑for‑byte against .las counterparts
// ---------------------------------------------------------------------------

func TestE2E_LAZ_vs_LAS(t *testing.T) {
	td := filepath.Join("testdata", "las")

	// Pre‑load all .las data
	lasData := make(map[string][]ptBuf)
	entries, err := os.ReadDir(td)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".las") || e.IsDir() {
			continue
		}
		t.Run(name+"_load", func(t *testing.T) {
			pts, u := readAll(t, filepath.Join(td, name))
			lasData[name] = pts
			u.Close()
		})
	}

	// Compare each .laz
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".laz") || e.IsDir() {
			continue
		}
		lasName := strings.TrimSuffix(name, ".laz") + ".las"

		t.Run(name, func(t *testing.T) {
			lasPts, ok := lasData[lasName]
			if !ok {
				t.Fatalf("no matching .las for %s", lasName)
			}

			lazPts, u := readAll(t, filepath.Join(td, name))
			defer u.Close()

			if len(lazPts) != len(lasPts) {
				t.Fatalf("count: laz=%d las=%d", len(lazPts), len(lasPts))
			}

			for pi := range lazPts {
				lb := lazPts[pi].buf
				sb := lasPts[pi].buf
				if len(lb) != len(sb) {
					t.Fatalf("pt %d buf len: laz=%d las=%d", pi, len(lb), len(sb))
				}
				for i := range lb {
					if lb[i] != sb[i] {
						t.Errorf("pt %d byte %d: laz=%02x las=%02x", pi, i, lb[i], sb[i])
					}
				}
			}
		})
	}
}
