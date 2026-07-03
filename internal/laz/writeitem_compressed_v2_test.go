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

// writeitem_compressed_v2_test.go — round-trip tests for the v2 compressed
// item writers (and the WAVEPACKET13 v1 writer), using the existing reader
// as the oracle: write with LASwritePoint, decode with LASreadPoint, and
// compare every point byte-for-byte.
package laz

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lasFixture holds the raw points of a .las file, split per item.
type lasFixture struct {
	points    [][][]byte // [point][item][]byte (in-memory item layout)
	items     []LASitem
	pointType uint8
	recordLen uint16
}

// loadLASFixture reads all points of a .las file via the (trusted) reader.
func loadLASFixture(t *testing.T, path string) *lasFixture {
	t.Helper()
	u, err := OpenLAS(path)
	if err != nil {
		t.Fatalf("OpenLAS %q: %v", path, err)
	}
	defer u.Close()

	items := u.Items()
	offsets := u.Offsets()
	var recordLen uint16
	for _, it := range items {
		recordLen += it.Size
	}

	n := int(u.NumPoints())
	points := make([][][]byte, n)
	for p := 0; p < n; p++ {
		bufs := make([][]byte, len(items))
		for i := range items {
			bufs[i] = make([]byte, offsets[i+1]-offsets[i])
		}
		if err := u.Read(bufs); err != nil {
			t.Fatalf("%s: read point %d: %v", path, p, err)
		}
		points[p] = bufs
	}
	return &lasFixture{points: points, items: items, pointType: u.PointFormat(), recordLen: recordLen}
}

// writeCompressed writes all points with LASwritePoint into out and Done()s.
func writeCompressed(t *testing.T, points [][][]byte, lz *LASzip, out ByteStreamOut) {
	t.Helper()
	wp := NewLASwritePoint()
	if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
		t.Fatalf("writer setup: %v", err)
	}
	if err := wp.Init(out); err != nil {
		t.Fatalf("writer init: %v", err)
	}
	for p, pt := range points {
		if err := wp.Write(pt); err != nil {
			t.Fatalf("write point %d: %v", p, err)
		}
	}
	if err := wp.Done(); err != nil {
		t.Fatalf("writer done: %v", err)
	}
}

// readBack decodes n points from data with the existing LASreadPoint.
func readBack(t *testing.T, data []byte, lz *LASzip, n int) [][][]byte {
	t.Helper()
	rp := NewLASreadPoint(LASZIP_DECOMPRESS_SELECTIVE_ALL)
	if err := rp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
		t.Fatalf("reader setup: %v", err)
	}
	if err := rp.Init(NewByteStreamInArray(data)); err != nil {
		t.Fatalf("reader init: %v", err)
	}
	points := make([][][]byte, n)
	for p := 0; p < n; p++ {
		bufs := make([][]byte, lz.NumItems)
		for i := range lz.Items {
			sz := uint32(lz.Items[i].Size)
			if lz.Items[i].Type == LASITEM_POINT14 {
				sz = 40
			}
			bufs[i] = make([]byte, sz)
		}
		if err := rp.Read(bufs); err != nil {
			t.Fatalf("read back point %d: %v", p, err)
		}
		points[p] = bufs
	}
	if err := rp.CheckEnd(); err != nil {
		t.Fatalf("reader CheckEnd: %v", err)
	}
	return points
}

// comparePoints compares two per-item point sets byte-for-byte.
func comparePoints(t *testing.T, want, got [][][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("point count mismatch: want %d got %d", len(want), len(got))
	}
	for p := range want {
		for i := range want[p] {
			if !bytes.Equal(want[p][i], got[p][i]) {
				t.Fatalf("point %d item %d mismatch:\nwant %x\n got %x", p, i, want[p][i], got[p][i])
			}
		}
	}
}

// TestWriterRoundTripLASFixtures round-trips every pf0-5 .las fixture:
// read raw → compress (POINTWISE_CHUNKED, chunk size 100, v2 items) →
// decompress with the existing reader → compare byte-for-byte.
func TestWriterRoundTripLASFixtures(t *testing.T) {
	td := filepath.Join("testdata", "las")
	entries, err := os.ReadDir(td)
	if err != nil {
		t.Fatalf("read testdata dir: %v", err)
	}
	ran := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".las") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			fx := loadLASFixture(t, filepath.Join(td, name))
			if fx.pointType > 5 {
				t.Skipf("point format %d uses layered v3/v4 writers (phase 3)", fx.pointType)
			}

			lz := NewLASzip()
			if err := lz.SetupByPointType(fx.pointType, fx.recordLen, LASZIP_COMPRESSOR_POINTWISE_CHUNKED); err != nil {
				t.Fatalf("SetupByPointType: %v", err)
			}
			if err := lz.SetChunkSize(100); err != nil {
				t.Fatalf("SetChunkSize: %v", err)
			}
			if err := lz.RequestVersion(2); err != nil {
				t.Fatalf("RequestVersion: %v", err)
			}

			out := NewByteStreamOutArray()
			writeCompressed(t, fx.points, lz, out)
			got := readBack(t, out.GetData(), lz, len(fx.points))
			comparePoints(t, fx.points, got)
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no .las fixtures found")
	}
}

// TestWriterGpsTime11v2MultiSequence exercises the GPSTIME11 v2 multi-
// sequence machinery: 32-bit diffs, multiplier branches (1, <10, >=10,
// MULTI extreme, negative, MULTI_MINUS extreme, zero), huge diffs starting
// new sequences, and switching back to a previous sequence (the recursive
// re-dispatch paths).
func TestWriterGpsTime11v2MultiSequence(t *testing.T) {
	mkPoint := func(x, y, z int32, gps float64) [][]byte {
		p10 := make([]byte, 20)
		binary.LittleEndian.PutUint32(p10[0:4], uint32(x))
		binary.LittleEndian.PutUint32(p10[4:8], uint32(y))
		binary.LittleEndian.PutUint32(p10[8:12], uint32(z))
		p10[14] = 1 | (1 << 3) // return 1 of 1
		gpsBuf := make([]byte, 8)
		binary.LittleEndian.PutUint64(gpsBuf, math.Float64bits(gps))
		return [][]byte{p10, gpsBuf}
	}

	var points [][][]byte
	// Sequence A: regular small increments (multi == 1 after the seed).
	ta := 100000.0
	for i := 0; i < 10; i++ {
		points = append(points, mkPoint(int32(i), int32(-i), 10, ta))
		ta += 0.001
	}
	// Vary the increment to hit multiplier branches.
	incs := []float64{0.002, 0.002, 0.005, 0.05, 0.9, 0.0000001, -0.003, -0.5, 0.001, 0.001}
	for _, inc := range incs {
		ta += inc
		points = append(points, mkPoint(1, 2, 3, ta))
	}
	// Extreme multipliers repeated (> 3 times triggers the last-diff reset).
	for i := 0; i < 6; i++ {
		ta += 10.0
		points = append(points, mkPoint(int32(i), 0, 0, ta))
	}
	// Sequence B: a huge jump in the double's bit pattern → new sequence.
	tb := 9.0e12
	points = append(points, mkPoint(0, 0, 0, tb))
	// Alternate between sequences A and B → "belongs to another sequence".
	for i := 0; i < 8; i++ {
		ta += 0.001
		tb += 0.25
		points = append(points, mkPoint(int32(i), int32(i), int32(i), ta))
		points = append(points, mkPoint(int32(-i), 0, 1, tb))
	}
	// A third sequence and an unchanged gpstime (special symbol).
	tc := -5.0e8
	points = append(points, mkPoint(0, 0, 0, tc))
	points = append(points, mkPoint(1, 1, 1, tc)) // unchanged
	points = append(points, mkPoint(2, 2, 2, ta)) // back to A
	points = append(points, mkPoint(3, 3, 3, tb)) // back to B

	lz := NewLASzip()
	if err := lz.SetupByPointType(1, 28, LASZIP_COMPRESSOR_POINTWISE_CHUNKED); err != nil {
		t.Fatalf("SetupByPointType: %v", err)
	}
	if err := lz.SetChunkSize(1000); err != nil { // single chunk
		t.Fatalf("SetChunkSize: %v", err)
	}

	out := NewByteStreamOutArray()
	writeCompressed(t, points, lz, out)
	got := readBack(t, out.GetData(), lz, len(points))
	comparePoints(t, points, got)
}
