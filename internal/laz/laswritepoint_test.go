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

// laswritepoint_test.go — LASwritePoint orchestrator tests: byte-exact
// golden comparison against the C++-produced .laz fixtures, chunk table
// round-trips, variable-size chunks, non-seekable output, and edge cases.
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

// parseLAZGolden minimally parses a .laz file: it returns the point data
// bytes (from offset_to_point_data to EOF — 8-byte chunk table pointer +
// chunks + chunk table), the offset itself, and the LASzip VLR configuration.
func parseLAZGolden(t *testing.T, path string) ([]byte, uint32, *LASzip) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	headerSize := binary.LittleEndian.Uint16(data[94:96])
	offset := binary.LittleEndian.Uint32(data[96:100])
	numVLRs := binary.LittleEndian.Uint32(data[100:104])

	var lz *LASzip
	pos := int(headerSize)
	for i := uint32(0); i < numVLRs; i++ {
		userID := strings.TrimRight(string(data[pos+2:pos+18]), "\x00")
		recordID := binary.LittleEndian.Uint16(data[pos+18 : pos+20])
		recordLen := int(binary.LittleEndian.Uint16(data[pos+20 : pos+22]))
		if userID == "laszip encoded" && recordID == 22204 {
			lz = NewLASzip()
			if err := lz.Unpack(data[pos+54 : pos+54+recordLen]); err != nil {
				t.Fatalf("unpack laszip VLR: %v", err)
			}
		}
		pos += 54 + recordLen
	}
	if lz == nil {
		t.Fatalf("%q: no laszip VLR found", path)
	}
	return data[offset:], offset, lz
}

// TestWriterGoldenByteExact writes the .las fixture points with the exact
// configuration of the corresponding C++-produced .laz fixture and compares
// the produced bytes (8-byte table pointer + chunks + chunk table) against
// the .laz point data byte-for-byte. This proves encoder fidelity.
func TestWriterGoldenByteExact(t *testing.T) {
	// testdata/las: laszip-CLI-written pf0-5 pairs.
	// testdata/cpporacle: harness-written multichannel pf7 v3 / pf8 v4 pairs
	// (the compat/ and corrupt/ subdirectories hold no comparable pairs).
	var pairs []struct{ dir, las string }
	for _, td := range []string{
		filepath.Join("testdata", "las"),
		filepath.Join("testdata", "cpporacle"),
	} {
		entries, err := os.ReadDir(td)
		if err != nil {
			t.Fatalf("read testdata dir: %v", err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".las") {
				continue
			}
			if _, err := os.Stat(filepath.Join(td, strings.TrimSuffix(name, ".las")+".laz")); err != nil {
				continue
			}
			pairs = append(pairs, struct{ dir, las string }{td, name})
		}
	}
	ran := 0
	for _, pair := range pairs {
		td, name := pair.dir, pair.las
		lazName := strings.TrimSuffix(name, ".las") + ".laz"
		t.Run(lazName, func(t *testing.T) {
			golden, offset, lz := parseLAZGolden(t, filepath.Join(td, lazName))

			if lz.Compressor != LASZIP_COMPRESSOR_POINTWISE_CHUNKED &&
				lz.Compressor != LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				t.Skipf("compressor %d (v1 pointwise writing is out of scope)", lz.Compressor)
			}
			for _, it := range lz.Items {
				if it.Version == 1 && it.Type != LASITEM_WAVEPACKET13 {
					t.Skipf("item type %d is version 1 (v1 writers are out of scope)", it.Type)
				}
			}

			fx := loadLASFixture(t, filepath.Join(td, name))

			out := NewByteStreamOutArray()
			// Pad the stream to the real file's offset_to_point_data so all
			// Tell() values (chunk table pointer, table position) match the
			// C++ writer's absolute file positions.
			if err := out.PutBytes(make([]byte, offset)); err != nil {
				t.Fatalf("pad: %v", err)
			}
			wp := NewLASwritePoint()
			if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
				t.Fatalf("writer setup: %v", err)
			}
			if err := wp.Init(out); err != nil {
				t.Fatalf("writer init: %v", err)
			}
			for p, pt := range fx.points {
				if err := wp.Write(pt); err != nil {
					t.Fatalf("write point %d: %v", p, err)
				}
			}
			if err := wp.Done(); err != nil {
				t.Fatalf("writer done: %v", err)
			}

			got := out.GetData()[offset:]
			if bytes.Equal(got, golden) {
				return
			}
			// Diagnose: first divergent byte and its chunk.
			n := min(len(got), len(golden))
			firstDiff := n
			for i := 0; i < n; i++ {
				if got[i] != golden[i] {
					firstDiff = i
					break
				}
			}
			chunk := -1
			if firstDiff >= 8 {
				chunk = 0
				pos := int64(8)
				for c, b := range wp.chunkBytes {
					if int64(firstDiff) < pos+int64(b) {
						chunk = c
						break
					}
					pos += int64(b)
					chunk = c + 1
				}
			}
			t.Fatalf("byte mismatch vs golden %s: len got=%d want=%d, first diff at offset %d (chunk %d): got %02x want %02x",
				lazName, len(got), len(golden), firstDiff, chunk, got[min(firstDiff, len(got)-1)], golden[min(firstDiff, len(golden)-1)])
		})
		ran++
	}
	if ran == 0 {
		t.Fatal("no .las/.laz fixture pairs found")
	}
}

// TestWriterChunkTableRoundTrip writes chunk tables through the writer's
// writeChunkTable path for synthetic chunk sizes/counts and reads them back
// with the reader's readChunkTable (white-box, same package).
func TestWriterChunkTableRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		chunkSize uint32 // MaxUint32 → variable-size chunking
		counts    []uint32
		bytes     []uint32
	}{
		{"fixed", 100, []uint32{100, 100, 42}, []uint32{1234, 999, 137}},
		{"fixed_one", 50000, []uint32{31}, []uint32{77}},
		{"variable", math.MaxUint32, []uint32{10, 25, 7, 1000}, []uint32{555, 3, 80000, 12}},
		{"empty", 100, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewByteStreamOutArray()
			wp := NewLASwritePoint()
			wp.enc = NewArithmeticEncoder()
			wp.chunkSize = tc.chunkSize
			wp.numberChunks = math.MaxUint32
			if err := wp.Init(out); err != nil {
				t.Fatalf("init: %v", err)
			}
			for i := range tc.bytes {
				// Simulate a chunk of tc.bytes[i] bytes on the stream.
				if err := out.PutBytes(make([]byte, tc.bytes[i])); err != nil {
					t.Fatalf("filler: %v", err)
				}
				wp.chunkCount = tc.counts[i]
				if err := wp.addChunkToTable(); err != nil {
					t.Fatalf("addChunkToTable: %v", err)
				}
			}
			if err := wp.writeChunkTable(); err != nil {
				t.Fatalf("writeChunkTable: %v", err)
			}

			// Read the table back.
			rp := NewLASreadPoint(LASZIP_DECOMPRESS_SELECTIVE_ALL)
			rp.dec = NewArithmeticDecoder()
			rp.chunkSize = tc.chunkSize
			rp.numberChunks = math.MaxUint32
			rp.instream = NewByteStreamInArray(out.GetData())
			if err := rp.readChunkTable(); err != nil {
				t.Fatalf("readChunkTable: %v", err)
			}
			if rp.lastWarning != "" {
				t.Fatalf("readChunkTable warning: %s", rp.lastWarning)
			}
			if rp.numberChunks != uint32(len(tc.bytes)) {
				t.Fatalf("numberChunks = %d, want %d", rp.numberChunks, len(tc.bytes))
			}
			// chunkStarts are absolute stream positions: chunks start at 8
			// (after the table pointer slot).
			wantStart := int64(8)
			if rp.chunkStarts[0] != wantStart {
				t.Fatalf("chunkStarts[0] = %d, want %d", rp.chunkStarts[0], wantStart)
			}
			for i := range tc.bytes {
				wantStart += int64(tc.bytes[i])
				if rp.chunkStarts[i+1] != wantStart {
					t.Errorf("chunkStarts[%d] = %d, want %d", i+1, rp.chunkStarts[i+1], wantStart)
				}
			}
			if tc.chunkSize == math.MaxUint32 {
				wantTotal := uint32(0)
				if rp.chunkTotals[0] != 0 {
					t.Errorf("chunkTotals[0] = %d, want 0", rp.chunkTotals[0])
				}
				for i := range tc.counts {
					wantTotal += tc.counts[i]
					if rp.chunkTotals[i+1] != wantTotal {
						t.Errorf("chunkTotals[%d] = %d, want %d", i+1, rp.chunkTotals[i+1], wantTotal)
					}
				}
			}
		})
	}
}

// fixturePoints loads the first n points of a 1000-point pf1 fixture.
func fixturePoints(t *testing.T, n int) ([][][]byte, *lasFixture) {
	t.Helper()
	fx := loadLASFixture(t, filepath.Join("testdata", "las", "las12_pf1_1000pts_with_extrabytes.las"))
	if len(fx.points) < n {
		t.Fatalf("fixture has %d points, need %d", len(fx.points), n)
	}
	return fx.points[:n], fx
}

// newFixtureLASzip builds a POINTWISE_CHUNKED LASzip config for a fixture.
func newFixtureLASzip(t *testing.T, fx *lasFixture, chunkSize uint32) *LASzip {
	t.Helper()
	lz := NewLASzip()
	if err := lz.SetupByPointType(fx.pointType, fx.recordLen, LASZIP_COMPRESSOR_POINTWISE_CHUNKED); err != nil {
		t.Fatalf("SetupByPointType: %v", err)
	}
	if err := lz.SetChunkSize(chunkSize); err != nil {
		t.Fatalf("SetChunkSize: %v", err)
	}
	return lz
}

// TestWriterVariableChunks writes points with explicit Chunk() calls at
// irregular boundaries (chunk size 0 → variable/adaptive chunking) and
// round-trips through the reader.
func TestWriterVariableChunks(t *testing.T) {
	points, fx := fixturePoints(t, 42)
	lz := newFixtureLASzip(t, fx, 0) // 0 → variable-size chunking

	out := NewByteStreamOutArray()
	wp := NewLASwritePoint()
	if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := wp.Init(out); err != nil {
		t.Fatalf("init: %v", err)
	}
	boundaries := map[int]bool{10: true, 35: true} // after points 10 and 35
	for p, pt := range points {
		if err := wp.Write(pt); err != nil {
			t.Fatalf("write point %d: %v", p, err)
		}
		if boundaries[p+1] {
			if err := wp.Chunk(); err != nil {
				t.Fatalf("chunk after point %d: %v", p, err)
			}
		}
	}
	if err := wp.Done(); err != nil {
		t.Fatalf("done: %v", err)
	}
	if wp.numberChunks != 3 {
		t.Fatalf("numberChunks = %d, want 3", wp.numberChunks)
	}
	if want := []uint32{10, 25, 7}; !equalU32(wp.chunkSizes, want) {
		t.Fatalf("chunk point counts = %v, want %v", wp.chunkSizes, want)
	}

	got := readBack(t, out.GetData(), lz, len(points))
	comparePoints(t, points, got)
}

func equalU32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWriterChunkOnFixedSizeFails verifies Chunk() is rejected unless
// variable-size chunking is active.
func TestWriterChunkOnFixedSizeFails(t *testing.T) {
	points, fx := fixturePoints(t, 1)
	lz := newFixtureLASzip(t, fx, 100)

	out := NewByteStreamOutArray()
	wp := NewLASwritePoint()
	if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := wp.Init(out); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := wp.Write(points[0]); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wp.Chunk(); err == nil {
		t.Fatal("Chunk() on fixed-size chunking should fail")
	}
}

// TestWriterNonSeekable writes to a non-seekable stream: the table pointer
// slot holds -1 and the real table position is appended after the table.
// The reader handles that layout.
func TestWriterNonSeekable(t *testing.T) {
	points, fx := fixturePoints(t, 250)
	lz := newFixtureLASzip(t, fx, 100)

	var buf bytes.Buffer
	out := NewByteStreamOutWriter(&buf)
	wp := NewLASwritePoint()
	if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := wp.Init(out); err != nil {
		t.Fatalf("init: %v", err)
	}
	for p, pt := range points {
		if err := wp.Write(pt); err != nil {
			t.Fatalf("write point %d: %v", p, err)
		}
	}
	if err := wp.Done(); err != nil {
		t.Fatalf("done: %v", err)
	}

	data := buf.Bytes()
	// The slot must hold -1 …
	if got := int64(binary.LittleEndian.Uint64(data[:8])); got != -1 {
		t.Fatalf("table pointer slot = %d, want -1", got)
	}
	// … and the last 8 bytes the appended table position.
	tablePos := int64(binary.LittleEndian.Uint64(data[len(data)-8:]))
	if tablePos <= 8 || tablePos >= int64(len(data)) {
		t.Fatalf("appended table position %d out of range", tablePos)
	}

	got := readBack(t, data, lz, len(points))
	comparePoints(t, points, got)
}

// TestWriterEdgeCases covers 0 points, a single point, exactly one full
// chunk, and a partial last chunk (fixed chunk size 100).
func TestWriterEdgeCases(t *testing.T) {
	allPoints, fx := fixturePoints(t, 250)
	lzFor := func() *LASzip { return newFixtureLASzip(t, fx, 100) }

	t.Run("zero_points", func(t *testing.T) {
		lz := lzFor()
		out := NewByteStreamOutArray()
		wp := NewLASwritePoint()
		if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := wp.Init(out); err != nil {
			t.Fatalf("init: %v", err)
		}
		if err := wp.Done(); err != nil {
			t.Fatalf("done: %v", err)
		}
		data := out.GetData()
		// 8-byte patched pointer + u32 version + u32 number_chunks(=0)
		if len(data) != 16 {
			t.Fatalf("empty stream length = %d, want 16", len(data))
		}
		if got := int64(binary.LittleEndian.Uint64(data[:8])); got != 8 {
			t.Fatalf("table pointer = %d, want 8", got)
		}
		if v := binary.LittleEndian.Uint32(data[8:12]); v != 0 {
			t.Fatalf("table version = %d, want 0", v)
		}
		if n := binary.LittleEndian.Uint32(data[12:16]); n != 0 {
			t.Fatalf("number chunks = %d, want 0", n)
		}
	})

	for _, tc := range []struct {
		name       string
		n          int
		wantChunks uint32
	}{
		{"single_point", 1, 1},
		{"exactly_one_full_chunk", 100, 1},
		{"partial_last_chunk", 250, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lz := lzFor()
			points := allPoints[:tc.n]
			out := NewByteStreamOutArray()
			wp := NewLASwritePoint()
			if err := wp.Setup(uint32(lz.NumItems), lz.Items, lz); err != nil {
				t.Fatalf("setup: %v", err)
			}
			if err := wp.Init(out); err != nil {
				t.Fatalf("init: %v", err)
			}
			for p, pt := range points {
				if err := wp.Write(pt); err != nil {
					t.Fatalf("write point %d: %v", p, err)
				}
			}
			if err := wp.Done(); err != nil {
				t.Fatalf("done: %v", err)
			}
			if wp.numberChunks != tc.wantChunks {
				t.Fatalf("numberChunks = %d, want %d", wp.numberChunks, tc.wantChunks)
			}
			got := readBack(t, out.GetData(), lz, tc.n)
			comparePoints(t, points, got)
		})
	}
}
