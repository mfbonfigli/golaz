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

// writer_test.go — tests for the high-level Writer API: full-fidelity
// round-trips over every fixture, byte-exactness against the C++-produced
// .laz goldens at the public level, synthetic from-scratch writing,
// inventory patch-back, variable chunking, scan-angle identity, and error
// cases.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	laz "github.com/mfbonfigli/golaz/internal/laz"
)

const oracleDir = "internal/laz/testdata/cpporacle"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type writerFixture struct {
	dir, name string
}

// writerFixtures lists every .las fixture in testdata/las and the cpporacle
// root (the corrupt/ and compat/ subdirectories are excluded by not
// recursing into them).
func writerFixtures(t *testing.T) []writerFixture {
	t.Helper()
	var out []writerFixture
	for _, dir := range []string{tdDir, oracleDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read fixture dir %q: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".las") {
				continue
			}
			out = append(out, writerFixture{dir, e.Name()})
		}
	}
	if len(out) == 0 {
		t.Fatal("no .las fixtures found")
	}
	return out
}

// writerHeaderFrom builds a WriterHeader replicating a source file's header.
func writerHeaderFrom(t *testing.T, sh *Header) WriterHeader {
	t.Helper()
	base, ok := pointFormatBaseSize(sh.PointDataFormat)
	if !ok {
		t.Fatalf("bad point format %d", sh.PointDataFormat)
	}
	// Some fixtures are deliberately non-standard (e.g. the LAS 1.0/1.1
	// files carry point format 4, which the spec introduced in 1.3). The
	// Writer enforces the spec, so target the minimum version that allows
	// the format.
	minor := sh.VersionMinor
	if (sh.PointDataFormat == 4 || sh.PointDataFormat == 5) && minor < 3 {
		minor = 3
	}
	return WriterHeader{
		VersionMinor:          minor,
		PointFormat:           sh.PointDataFormat,
		ExtraByteCount:        sh.PointDataRecordLength - base,
		ScaleX:                sh.ScaleX,
		ScaleY:                sh.ScaleY,
		ScaleZ:                sh.ScaleZ,
		OffsetX:               sh.OffsetX,
		OffsetY:               sh.OffsetY,
		OffsetZ:               sh.OffsetZ,
		FileSourceID:          sh.FileSourceID,
		GlobalEncoding:        sh.GlobalEncoding,
		FileCreationDayOfYear: sh.FileCreationDayOfYear,
		FileCreationYear:      sh.FileCreationYear,
	}
}

// writeAll streams pts into w and closes it.
func writeAll(t *testing.T, w *Writer, pts []*Point) {
	t.Helper()
	for i, p := range pts {
		if err := w.WritePoint(p); err != nil {
			t.Fatalf("WritePoint %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// comparePointsRaw asserts each reread point's Raw bytes equal the source's.
func comparePointsRaw(t *testing.T, r *Reader, pts []*Point) {
	t.Helper()
	var q Point
	for i := range pts {
		if err := r.Scan(&q); err != nil {
			t.Fatalf("Scan %d: %v", i, err)
		}
		if !bytes.Equal(q.Raw(), pts[i].Raw()) {
			t.Fatalf("point %d: raw bytes differ\n got %x\nwant %x", i, q.Raw(), pts[i].Raw())
		}
	}
	if err := r.Scan(&q); err != io.EOF {
		t.Fatalf("expected EOF after %d points, got %v", len(pts), err)
	}
}

// ---------------------------------------------------------------------------
// 1. Full-fidelity round trip over every fixture (.las and .laz output)
// ---------------------------------------------------------------------------

func TestWriter_RoundTrip_AllFixtures(t *testing.T) {
	for _, fx := range writerFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			r, err := Open(filepath.Join(fx.dir, fx.name))
			if err != nil {
				t.Fatalf("Open source: %v", err)
			}
			sh := r.Header()
			pts := scanAll(t, r)
			r.Close()

			hdr := writerHeaderFrom(t, sh)
			for _, ext := range []string{".las", ".laz"} {
				t.Run(ext, func(t *testing.T) {
					out := filepath.Join(t.TempDir(), "out"+ext)
					var opts []WriterOption
					if ext == ".laz" {
						opts = append(opts, WithChunkSize(100))
					}
					w, err := Create(out, hdr, opts...)
					if err != nil {
						t.Fatalf("Create: %v", err)
					}
					writeAll(t, w, pts)

					r2, err := Open(out)
					if err != nil {
						t.Fatalf("reopen: %v", err)
					}
					defer r2.Close()
					h2 := r2.Header()
					if h2.IsCompressed != (ext == ".laz") {
						t.Errorf("IsCompressed = %v for %s output", h2.IsCompressed, ext)
					}
					if h2.NumberOfPoints != sh.NumberOfPoints {
						t.Errorf("NumberOfPoints = %d, want %d", h2.NumberOfPoints, sh.NumberOfPoints)
					}
					if h2.PointsByReturn != sh.PointsByReturn {
						t.Errorf("PointsByReturn = %v, want %v", h2.PointsByReturn, sh.PointsByReturn)
					}
					for _, bb := range []struct {
						name      string
						got, want float64
					}{
						{"MinX", h2.MinX, sh.MinX}, {"MaxX", h2.MaxX, sh.MaxX},
						{"MinY", h2.MinY, sh.MinY}, {"MaxY", h2.MaxY, sh.MaxY},
						{"MinZ", h2.MinZ, sh.MinZ}, {"MaxZ", h2.MaxZ, sh.MaxZ},
					} {
						if !withinTol(bb.got, bb.want, 1e-9) {
							t.Errorf("bbox %s = %v, want %v", bb.name, bb.got, bb.want)
						}
					}
					comparePointsRaw(t, r2, pts)
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Byte-exactness against the C++-produced .laz goldens (public API level)
// ---------------------------------------------------------------------------

// TestWriter_ByteExact_Golden writes fixture points through the public API
// with the golden's configuration (same VLRs, same chunk size) and compares
// the produced compressed point data against the fixture .laz byte for byte
// from offset_to_point_data to EOF. The 8-byte chunk-table pointer is an
// absolute file position, so it is compared relative to the point-data
// offset (which makes the test robust to VLR-section size differences).
func TestWriter_ByteExact_Golden(t *testing.T) {
	cases := []struct {
		dir, base string
	}{
		{tdDir, "las12_pf3_1000pts_with_extrabytes"},
		{tdDir, "las14_pf6_1000pts_with_extrabytes"},
		// pf7 v3 multichannel uses the golaz defaults (v3 items, chunked);
		// the pf8_v4 sibling needs item version 4, which the public API only
		// selects for LAS 1.5, so it is exercised at the engine level instead.
		{oracleDir, "las14_pf7_v3_multichannel_1000pts"},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(tc.dir, tc.base+".laz"))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			wantOff := binary.LittleEndian.Uint32(want[96:100])

			// Source points from the .las twin.
			r, err := Open(filepath.Join(tc.dir, tc.base+".las"))
			if err != nil {
				t.Fatalf("Open source: %v", err)
			}
			sh := r.Header()
			pts := scanAll(t, r)
			r.Close()

			// Golden's user VLRs and chunk size from the .laz.
			rz, err := Open(filepath.Join(tc.dir, tc.base+".laz"))
			if err != nil {
				t.Fatalf("Open golden: %v", err)
			}
			var userVLRs []VLR
			var chunkSize uint32
			for _, v := range rz.VLRs() {
				if v.IsLASzip() {
					lz := laz.NewLASzip()
					if err := lz.Unpack(v.Data); err != nil {
						t.Fatalf("unpack golden LASzip VLR: %v", err)
					}
					chunkSize = lz.ChunkSize
					continue
				}
				userVLRs = append(userVLRs, v)
			}
			rz.Close()
			if chunkSize == 0 {
				t.Fatal("golden has no LASzip VLR chunk size")
			}

			hdr := writerHeaderFrom(t, sh)
			hdr.VLRs = userVLRs
			out := filepath.Join(t.TempDir(), "out.laz")
			w, err := Create(out, hdr, WithChunkSize(chunkSize))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			writeAll(t, w, pts)

			got, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			gotOff := binary.LittleEndian.Uint32(got[96:100])

			gotPtr := int64(binary.LittleEndian.Uint64(got[gotOff : gotOff+8]))
			wantPtr := int64(binary.LittleEndian.Uint64(want[wantOff : wantOff+8]))
			if gotPtr-int64(gotOff) != wantPtr-int64(wantOff) {
				t.Fatalf("chunk table pointer: got %d (offset %d), want %d (offset %d)",
					gotPtr, gotOff, wantPtr, wantOff)
			}
			g, wnt := got[gotOff+8:], want[wantOff+8:]
			if !bytes.Equal(g, wnt) {
				n := min(len(g), len(wnt))
				firstDiff := n
				for i := 0; i < n; i++ {
					if g[i] != wnt[i] {
						firstDiff = i
						break
					}
				}
				t.Fatalf("point data differs from golden: len got=%d want=%d, first diff at point-data offset %d",
					len(g), len(wnt), firstDiff+8)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Constructed-from-scratch synthetic points
// ---------------------------------------------------------------------------

func TestWriter_Synthetic_FromScratch(t *testing.T) {
	const n = 1000
	out := filepath.Join(t.TempDir(), "synth.laz")
	hdr := WriterHeader{
		PointFormat:    7,
		ExtraByteCount: 4,
		ScaleX:         0.01, ScaleY: 0.01, ScaleZ: 0.01,
		OffsetX: 1000, OffsetY: -2000, OffsetZ: 50,
	}
	w, err := Create(out, hdr, WithChunkSize(64))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	type expected struct {
		rawX, rawY, rawZ int32
		gps              float64
		r, g, b          uint16
		chan_            uint8
		angle            float64
		extra            [4]byte
	}
	exp := make([]expected, n)
	for i := 0; i < n; i++ {
		p := NewPoint(7)
		x := 1000.0 + float64(i)*0.037
		y := -2000.0 + float64(i%97)*1.5
		z := 50.0 + math.Sin(float64(i))*10
		w.SetCoordinates(p, x, y, z)
		p.Intensity = uint16(i * 7)
		p.ReturnNumber = uint8(1 + i%15)
		p.NumberOfReturns = uint8(1 + i%15)
		p.Classification = uint8(i % 256)
		p.ClassificationFlags = uint8(i % 16)
		p.ScanDirectionFlag = i%2 == 0
		p.EdgeOfFlightLine = i%5 == 0
		p.UserData = uint8(i % 251)
		p.PointSourceID = uint16(i)
		angle := float64(int16(i%30000)-15000) * 0.006
		p.ScanAngleDegrees = angle
		p.SetGPSTime(1e5 + float64(i)*0.001)
		p.SetRGB(uint16(i), uint16(i*2), uint16(i*3))
		p.SetScannerChannel(uint8(i % 4)) // multi-channel

		eb := []byte{byte(i), byte(i >> 8), 0xAB, byte(i % 7)}
		p.SetExtraBytes(eb)
		// SetExtraBytes must copy: mutating the source afterwards must not
		// affect the point.
		eb[0] = 0xFF

		// Has* coherence on the constructed point.
		if !p.HasGPSTime() || !p.HasRGB() || !p.HasExtendedFields() || !p.HasExtraBytes() {
			t.Fatal("constructed point: expected presence bits not set")
		}
		if p.HasNIR() || p.HasWavepacket() {
			t.Fatal("constructed point: unexpected presence bits set for pf7")
		}

		e := &exp[i]
		e.rawX, e.rawY, e.rawZ = p.RawX, p.RawY, p.RawZ
		e.gps = 1e5 + float64(i)*0.001
		e.r, e.g, e.b = uint16(i), uint16(i*2), uint16(i*3)
		e.chan_ = uint8(i % 4)
		e.angle = angle
		e.extra = [4]byte{byte(i), byte(i >> 8), 0xAB, byte(i % 7)}

		if err := w.WritePoint(p); err != nil {
			t.Fatalf("WritePoint %d: %v", i, err)
		}
	}
	if w.Tell() != n {
		t.Fatalf("Tell() = %d, want %d", w.Tell(), n)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	if r.NumPoints() != n {
		t.Fatalf("NumPoints = %d, want %d", r.NumPoints(), n)
	}
	if r.Header().PointDataFormat != 7 {
		t.Fatalf("PointDataFormat = %d, want 7", r.Header().PointDataFormat)
	}
	var q Point
	for i := 0; i < n; i++ {
		if err := r.Scan(&q); err != nil {
			t.Fatalf("Scan %d: %v", i, err)
		}
		e := &exp[i]
		if q.RawX != e.rawX || q.RawY != e.rawY || q.RawZ != e.rawZ {
			t.Fatalf("point %d: raw coords (%d,%d,%d), want (%d,%d,%d)",
				i, q.RawX, q.RawY, q.RawZ, e.rawX, e.rawY, e.rawZ)
		}
		if q.Intensity != uint16(i*7) || q.ReturnNumber != uint8(1+i%15) ||
			q.NumberOfReturns != uint8(1+i%15) || q.Classification != uint8(i%256) ||
			q.ClassificationFlags != uint8(i%16) || q.UserData != uint8(i%251) ||
			q.PointSourceID != uint16(i) ||
			q.ScanDirectionFlag != (i%2 == 0) || q.EdgeOfFlightLine != (i%5 == 0) {
			t.Fatalf("point %d: universal field mismatch: %+v", i, q)
		}
		if q.ScanAngleDegrees != e.angle {
			t.Fatalf("point %d: scan angle %v, want %v", i, q.ScanAngleDegrees, e.angle)
		}
		if gps, ok := q.GPSTime(); !ok || gps != e.gps {
			t.Fatalf("point %d: gps (%v, %v), want (%v, true)", i, gps, ok, e.gps)
		}
		if rr, gg, bb, ok := q.RGB(); !ok || rr != e.r || gg != e.g || bb != e.b {
			t.Fatalf("point %d: rgb (%d,%d,%d,%v), want (%d,%d,%d,true)", i, rr, gg, bb, ok, e.r, e.g, e.b)
		}
		if ch, ok := q.ScannerChannel(); !ok || ch != e.chan_ {
			t.Fatalf("point %d: channel (%d, %v), want (%d, true)", i, ch, ok, e.chan_)
		}
		if !q.HasExtraBytes() || !bytes.Equal(q.ExtraBytes(), e.extra[:]) {
			t.Fatalf("point %d: extra bytes %x, want %x", i, q.ExtraBytes(), e.extra)
		}
		if q.HasNIR() || q.HasWavepacket() {
			t.Fatalf("point %d: unexpected NIR/wavepacket presence for pf7", i)
		}
	}
}

// ---------------------------------------------------------------------------
// 4. Inventory patch-back and non-seekable output
// ---------------------------------------------------------------------------

func TestWriter_InventoryPatchBack(t *testing.T) {
	r, err := Open(filepath.Join(tdDir, "las12_pf1_1000pts_with_extrabytes.las"))
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	sh := r.Header()
	pts := scanAll(t, r)[:250]
	r.Close()

	// Ground truth inventory from the points themselves.
	var wantByReturn [15]uint64
	minX, minY, minZ := math.Inf(1), math.Inf(1), math.Inf(1)
	maxX, maxY, maxZ := math.Inf(-1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		if rn := p.ReturnNumber & 0x07; rn >= 1 {
			wantByReturn[rn-1]++
		}
		x := float64(p.RawX)*sh.ScaleX + sh.OffsetX
		y := float64(p.RawY)*sh.ScaleY + sh.OffsetY
		z := float64(p.RawZ)*sh.ScaleZ + sh.OffsetZ
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
		minZ, maxZ = math.Min(minZ, z), math.Max(maxZ, z)
	}

	hdr := writerHeaderFrom(t, sh) // NumberOfPoints deliberately not declared
	for _, ext := range []string{".las", ".laz"} {
		t.Run(ext, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out"+ext)
			w, err := Create(out, hdr)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			writeAll(t, w, pts)

			r2, err := Open(out)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer r2.Close()
			h2 := r2.Header()
			if h2.NumberOfPoints != 250 {
				t.Errorf("NumberOfPoints = %d, want 250", h2.NumberOfPoints)
			}
			if h2.PointsByReturn != wantByReturn {
				t.Errorf("PointsByReturn = %v, want %v", h2.PointsByReturn, wantByReturn)
			}
			for _, bb := range []struct {
				name      string
				got, want float64
			}{
				{"MinX", h2.MinX, minX}, {"MaxX", h2.MaxX, maxX},
				{"MinY", h2.MinY, minY}, {"MaxY", h2.MaxY, maxY},
				{"MinZ", h2.MinZ, minZ}, {"MaxZ", h2.MaxZ, maxZ},
			} {
				if bb.got != bb.want {
					t.Errorf("bbox %s = %v, want %v", bb.name, bb.got, bb.want)
				}
			}
		})
	}
}

func TestWriter_NonSeekable(t *testing.T) {
	r, err := Open(filepath.Join(tdDir, "las12_pf1_1000pts_with_extrabytes.las"))
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	sh := r.Header()
	pts := scanAll(t, r)[:250]
	r.Close()

	t.Run("declared_count_works", func(t *testing.T) {
		hdr := writerHeaderFrom(t, sh)
		hdr.NumberOfPoints = uint64(len(pts))
		var buf bytes.Buffer // io.Writer only — not seekable
		w, err := NewWriter(&buf, hdr, WithCompression(true), WithChunkSize(100))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		writeAll(t, w, pts)

		r2, err := OpenReader(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer r2.Close()
		if r2.NumPoints() != uint64(len(pts)) {
			t.Fatalf("NumPoints = %d, want %d", r2.NumPoints(), len(pts))
		}
		comparePointsRaw(t, r2, pts)
	})

	t.Run("undeclared_count_errors_on_close", func(t *testing.T) {
		hdr := writerHeaderFrom(t, sh) // NumberOfPoints = 0
		var buf bytes.Buffer
		w, err := NewWriter(&buf, hdr, WithCompression(true))
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		for i, p := range pts[:10] {
			if err := w.WritePoint(p); err != nil {
				t.Fatalf("WritePoint %d: %v", i, err)
			}
		}
		if err := w.Close(); err == nil {
			t.Fatal("Close on non-seekable output without a declared point count should fail")
		}
	})

	t.Run("mismatched_count_errors_on_close", func(t *testing.T) {
		hdr := writerHeaderFrom(t, sh)
		hdr.NumberOfPoints = 42
		var buf bytes.Buffer
		w, err := NewWriter(&buf, hdr)
		if err != nil {
			t.Fatalf("NewWriter: %v", err)
		}
		for i, p := range pts[:10] {
			if err := w.WritePoint(p); err != nil {
				t.Fatalf("WritePoint %d: %v", i, err)
			}
		}
		if err := w.Close(); err == nil {
			t.Fatal("Close with a mismatched declared point count should fail")
		}
	})
}

// ---------------------------------------------------------------------------
// 5. Variable-size chunking with explicit Chunk() calls
// ---------------------------------------------------------------------------

func TestWriter_VariableChunking(t *testing.T) {
	r, err := Open(filepath.Join(tdDir, "las14_pf6_1000pts_with_extrabytes.las"))
	if err != nil {
		t.Fatalf("Open source: %v", err)
	}
	sh := r.Header()
	pts := scanAll(t, r)[:200]
	r.Close()

	out := filepath.Join(t.TempDir(), "out.laz")
	w, err := Create(out, writerHeaderFrom(t, sh), WithChunkSize(0))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boundaries := map[int]bool{50: true, 125: true}
	for i, p := range pts {
		if err := w.WritePoint(p); err != nil {
			t.Fatalf("WritePoint %d: %v", i, err)
		}
		if boundaries[i+1] {
			if err := w.Chunk(); err != nil {
				t.Fatalf("Chunk after point %d: %v", i, err)
			}
			// A second Chunk with no interleaving points is a no-op.
			if err := w.Chunk(); err != nil {
				t.Fatalf("no-op Chunk: %v", err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r2, err := Open(out)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r2.Close()
	comparePointsRaw(t, r2, pts)

	// Random access must work across the explicit chunk boundaries.
	if err := r2.Seek(150); err != nil {
		t.Fatalf("Seek(150): %v", err)
	}
	var q Point
	if err := r2.Scan(&q); err != nil {
		t.Fatalf("Scan after seek: %v", err)
	}
	if !bytes.Equal(q.Raw(), pts[150].Raw()) {
		t.Fatal("point 150 mismatch after Seek")
	}
}

// ---------------------------------------------------------------------------
// 6. Scan-angle round-trip identity (reader → writer encoding → reader)
// ---------------------------------------------------------------------------

func TestWriter_ScanAngle_RoundTripIdentity(t *testing.T) {
	for _, fx := range writerFixtures(t) {
		t.Run(fx.name, func(t *testing.T) {
			r, err := Open(filepath.Join(fx.dir, fx.name))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			pf := r.Header().PointDataFormat
			var p Point
			for i := 0; ; i++ {
				err := r.Scan(&p)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("Scan %d: %v", i, err)
				}
				var back float64
				if pf <= 5 {
					back = float64(scanAngleRankFromDegrees(p.ScanAngleDegrees))
				} else {
					back = float64(scanAngleI16FromDegrees(p.ScanAngleDegrees)) * 0.006
				}
				if back != p.ScanAngleDegrees {
					t.Fatalf("point %d: scan angle %v does not round-trip (writer would store %v)",
						i, p.ScanAngleDegrees, back)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. Error cases
// ---------------------------------------------------------------------------

func TestWriter_Errors(t *testing.T) {
	dir := t.TempDir()
	path := func(name string) string { return filepath.Join(dir, name) }

	t.Run("bad_point_format", func(t *testing.T) {
		if _, err := Create(path("a.las"), WriterHeader{PointFormat: 11}); err == nil {
			t.Fatal("point format 11 should fail")
		}
	})

	t.Run("pf6_needs_las14", func(t *testing.T) {
		if _, err := Create(path("b.las"), WriterHeader{PointFormat: 6, VersionMinor: 2}); err == nil {
			t.Fatal("point format 6 with LAS 1.2 should fail")
		}
	})

	t.Run("pf4_needs_las13", func(t *testing.T) {
		if _, err := Create(path("c.las"), WriterHeader{PointFormat: 4, VersionMinor: 2}); err == nil {
			t.Fatal("point format 4 with LAS 1.2 should fail")
		}
	})

	t.Run("record_length_over_limit", func(t *testing.T) {
		if _, err := Create(path("d.las"), WriterHeader{PointFormat: 0, ExtraByteCount: 981}); err == nil {
			t.Fatal("record length > 1000 should fail")
		}
	})

	t.Run("laszip_vlr_supplied", func(t *testing.T) {
		hdr := WriterHeader{PointFormat: 0, VLRs: []VLR{{UserID: "laszip encoded", RecordID: 22204}}}
		if _, err := Create(path("e.laz"), hdr); err == nil {
			t.Fatal("user-supplied LASzip VLR should fail")
		}
	})

	t.Run("write_after_close", func(t *testing.T) {
		w, err := Create(path("f.las"), WriterHeader{PointFormat: 0})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := w.WritePoint(NewPoint(0)); err != nil {
			t.Fatalf("WritePoint: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := w.WritePoint(NewPoint(0)); err == nil {
			t.Fatal("WritePoint after Close should fail")
		}
		if err := w.Close(); err != nil {
			t.Fatalf("second Close should be a no-op, got %v", err)
		}
	})

	t.Run("chunk_with_fixed_chunking", func(t *testing.T) {
		w, err := Create(path("g.laz"), WriterHeader{PointFormat: 0}, WithChunkSize(100))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer w.Close()
		if err := w.WritePoint(NewPoint(0)); err != nil {
			t.Fatalf("WritePoint: %v", err)
		}
		if err := w.Chunk(); err == nil {
			t.Fatal("Chunk() with fixed-size chunking should fail")
		}
	})

	t.Run("chunk_on_uncompressed", func(t *testing.T) {
		w, err := Create(path("h.las"), WriterHeader{PointFormat: 0})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer w.Close()
		if err := w.Chunk(); err == nil {
			t.Fatal("Chunk() on uncompressed output should fail")
		}
	})
}

// ---------------------------------------------------------------------------
// 8. Extension-based compression inference
// ---------------------------------------------------------------------------

func TestWriter_CompressionInference(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name       string
		opts       []WriterOption
		compressed bool
	}{
		{"a.las", nil, false},
		{"b.laz", nil, true},
		{"c.LAZ", nil, true},
		{"d.las", []WriterOption{WithCompression(true)}, true},
		{"e.laz", []WriterOption{WithCompression(false)}, false},
	} {
		t.Run(fmt.Sprintf("%s_compressed_%v", tc.name, tc.compressed), func(t *testing.T) {
			out := filepath.Join(dir, tc.name)
			w, err := Create(out, WriterHeader{PointFormat: 1}, tc.opts...)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			p := NewPoint(1)
			p.RawX, p.RawY, p.RawZ = 1, 2, 3
			p.ReturnNumber, p.NumberOfReturns = 1, 1
			p.SetGPSTime(123.456)
			if err := w.WritePoint(p); err != nil {
				t.Fatalf("WritePoint: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			r, err := Open(out)
			if err != nil {
				t.Fatalf("reopen: %v", err)
			}
			defer r.Close()
			if r.Header().IsCompressed != tc.compressed {
				t.Fatalf("IsCompressed = %v, want %v", r.Header().IsCompressed, tc.compressed)
			}
			var q Point
			if err := r.Scan(&q); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if q.RawX != 1 || q.RawY != 2 || q.RawZ != 3 {
				t.Fatalf("coords (%d,%d,%d), want (1,2,3)", q.RawX, q.RawY, q.RawZ)
			}
			if gps, ok := q.GPSTime(); !ok || gps != 123.456 {
				t.Fatalf("gps (%v,%v), want (123.456,true)", gps, ok)
			}
		})
	}
}
