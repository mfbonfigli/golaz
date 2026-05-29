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

// Selective decompression tests for LAS 1.4 layered chunked LAZ files.
//
// LAS 1.4 v3 decompressors support LASZIP_DECOMPRESS_SELECTIVE_* masks:
// when a bit is clear the corresponding layer is not loaded from the stream
// at all, and the attribute keeps its "seed" value until the next chunk
// boundary.  At each chunk boundary the first point is always read RAW
// (uncompressed), so the seed resets to that chunk's actual first-point value.
//
// Therefore the invariant is:
//   - Within a chunk, a non-requested attribute is CONSTANT (frozen at the
//     seed = first point of that chunk).
//   - Across chunk boundaries the frozen value may differ.
//   - Requested attributes decode correctly for every point.
//
// Only LAS 1.4 (v3/v4 compressors) supports selective decompression.
// LAS 1.2/1.3 always decompress everything regardless of the mask.

import (
	"bytes"
	"path/filepath"
	"testing"
)

const selTestFile60k = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"

// chunkSize60k is the chunk size encoded in the test file (100 points).
const chunkSize60k = uint32(100)

// openSelective opens a LAZ file with a custom decompressSelective mask and
// reads all points, returning one flat []byte per point.
func openSelective(t *testing.T, path string, mask uint32) [][]byte {
	t.Helper()
	u, err := OpenLASSelective(path, mask)
	if err != nil {
		t.Fatalf("OpenLASSelective(%q, 0x%08x): %v", path, mask, err)
	}
	defer u.Close()

	items := u.Items()
	offsets := u.Offsets()
	total := offsets[len(offsets)-1]
	n := u.NumPoints()

	out := make([][]byte, n)
	for i := range n {
		buf := make([]byte, total)
		pt := make([][]byte, len(items))
		for j := range items {
			pt[j] = buf[offsets[j]:offsets[j+1]]
		}
		if err := u.Read(pt); err != nil {
			t.Fatalf("selective read pt %d: %v", i, err)
		}
		out[i] = buf
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers for reading fields out of the POINT14 in-memory layout
// ---------------------------------------------------------------------------

func pt14Z(b []byte) int32 {
	return int32(uint32(b[8]) | uint32(b[9])<<8 | uint32(b[10])<<16 | uint32(b[11])<<24)
}
func pt14X(b []byte) int32 {
	return int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)
}
func pt14Intensity(b []byte) uint16 { return uint16(b[12]) | uint16(b[13])<<8 }
func pt14GPSf(b []byte) float64     { return p14GPS(b) }
func extraBytes14(b []byte) []byte  { return b[40:] }

// chunkSeedIdx returns the index of the first point of the chunk that contains
// point i, given a fixed chunkSize.
func chunkSeedIdx(i, chunkSize uint32) uint32 {
	return (i / chunkSize) * chunkSize
}

// ---------------------------------------------------------------------------
// Test: Z not requested — within each chunk Z is frozen at that chunk's seed.
//       Requested attributes (X) still decode correctly for every point.
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_SkipZ(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	// Ground truth: all attributes.
	full := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_ALL)

	// Selective: skip Z only.
	mask := LASZIP_DECOMPRESS_SELECTIVE_ALL &^ LASZIP_DECOMPRESS_SELECTIVE_Z
	sel := openSelective(t, path, mask)

	xDiffers := false
	for i := uint32(0); i < uint32(len(sel)); i++ {
		// Expected frozen Z = Z of the first point in this chunk (the raw seed).
		seedZ := pt14Z(full[chunkSeedIdx(i, chunkSize60k)])
		if z := pt14Z(sel[i]); z != seedZ {
			t.Errorf("pt %d: Z=%d want frozen=%d (chunk seed pt %d)",
				i, z, seedZ, chunkSeedIdx(i, chunkSize60k))
		}
		// X must still match full decompression.
		if x := pt14X(sel[i]); x != pt14X(full[i]) {
			t.Errorf("pt %d: X=%d (selective) != %d (full)", i, x, pt14X(full[i]))
		}
		if i > 0 && pt14X(sel[i]) != pt14X(sel[0]) {
			xDiffers = true
		}
	}
	if !xDiffers {
		t.Error("X never changed across 1000 points — test data may be degenerate")
	}
}

// ---------------------------------------------------------------------------
// Test: GPS time not requested — GPS frozen per chunk, Z decodes correctly.
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_SkipGPS(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	full := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_ALL)

	mask := LASZIP_DECOMPRESS_SELECTIVE_ALL &^ LASZIP_DECOMPRESS_SELECTIVE_GPS_TIME
	sel := openSelective(t, path, mask)

	zDiffers := false
	for i := uint32(0); i < uint32(len(sel)); i++ {
		seedGPS := pt14GPSf(full[chunkSeedIdx(i, chunkSize60k)])
		if g := pt14GPSf(sel[i]); g != seedGPS {
			t.Errorf("pt %d: GPS %.6f want frozen=%.6f (chunk seed pt %d)",
				i, g, seedGPS, chunkSeedIdx(i, chunkSize60k))
		}
		// Z must still match full decompression.
		if z := pt14Z(sel[i]); z != pt14Z(full[i]) {
			t.Errorf("pt %d: Z=%d (selective) != %d (full)", i, z, pt14Z(full[i]))
		}
		if i > 0 && pt14Z(sel[i]) != pt14Z(sel[0]) {
			zDiffers = true
		}
	}
	if !zDiffers {
		t.Error("Z never changed — test data may be degenerate")
	}
}

// ---------------------------------------------------------------------------
// Test: Intensity not requested — frozen per chunk.
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_SkipIntensity(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	full := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_ALL)

	mask := LASZIP_DECOMPRESS_SELECTIVE_ALL &^ LASZIP_DECOMPRESS_SELECTIVE_INTENSITY
	sel := openSelective(t, path, mask)

	for i := uint32(0); i < uint32(len(sel)); i++ {
		seedInt := pt14Intensity(full[chunkSeedIdx(i, chunkSize60k)])
		if v := pt14Intensity(sel[i]); v != seedInt {
			t.Errorf("pt %d: intensity=%d want frozen=%d (chunk seed pt %d)",
				i, v, seedInt, chunkSeedIdx(i, chunkSize60k))
		}
		// Z still decompresses correctly.
		if z := pt14Z(sel[i]); z != pt14Z(full[i]) {
			t.Errorf("pt %d: Z mismatch under intensity skip", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: Extra bytes not requested — frozen per chunk, core fields change.
// The test file has 8 extra bytes (GridID uint32 + Confidence float32).
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_SkipExtraBytes(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	full := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_ALL)

	// LASZIP_DECOMPRESS_SELECTIVE_EXTRA_BYTES = 0xFFFF0000 covers all extra byte slots.
	mask := LASZIP_DECOMPRESS_SELECTIVE_ALL &^ LASZIP_DECOMPRESS_SELECTIVE_EXTRA_BYTES
	sel := openSelective(t, path, mask)

	xDiffers := false
	for i := uint32(0); i < uint32(len(sel)); i++ {
		// Seed extra bytes come from the raw first point of the chunk.
		seedIdx := chunkSeedIdx(i, chunkSize60k)
		seedExtra := extraBytes14(full[seedIdx])
		if !bytes.Equal(extraBytes14(sel[i]), seedExtra) {
			t.Errorf("pt %d: extra bytes differ from chunk seed (pt %d)", i, seedIdx)
		}
		// X still decompresses correctly.
		if x := pt14X(sel[i]); x != pt14X(full[i]) {
			t.Errorf("pt %d: X mismatch under extra-byte skip", i)
		}
		if i > 0 && pt14X(sel[i]) != pt14X(sel[0]) {
			xDiffers = true
		}
	}
	if !xDiffers {
		t.Error("X never changed — test data may be degenerate")
	}
}

// ---------------------------------------------------------------------------
// Test: ONLY Z requested (mask = LASZIP_DECOMPRESS_SELECTIVE_Z = 0x1).
//
// Important: X/Y/channel/returns live in the "channel_returns_XY" layer which
// has NO selective bit (LASZIP_DECOMPRESS_SELECTIVE_CHANNEL_RETURNS_XY = 0).
// That layer is ALWAYS decoded regardless of the mask — it carries the scanner-
// channel context that the whole decompressor depends on.  Therefore:
//   - X is NOT frozen; it decodes correctly for every point.
//   - Z is also decoded correctly (the Z bit is set).
//   - GPS, classification, etc. ARE frozen at the chunk seed (bits not set).
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_OnlyZ(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	full := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_ALL)
	sel := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_Z)

	zDiffers := false
	for i := uint32(0); i < uint32(len(sel)); i++ {
		// Z must match full decompression exactly.
		if z := pt14Z(sel[i]); z != pt14Z(full[i]) {
			t.Errorf("pt %d: Z=%d want %d", i, z, pt14Z(full[i]))
		}
		// X must ALSO match full: the XY layer is always decoded (no selective bit).
		if x := pt14X(sel[i]); x != pt14X(full[i]) {
			t.Errorf("pt %d: X=%d want %d (XY layer always decoded)", i, x, pt14X(full[i]))
		}
		// GPS must be frozen at the chunk seed (GPS bit not set in mask).
		seedGPS := pt14GPSf(full[chunkSeedIdx(i, chunkSize60k)])
		if g := pt14GPSf(sel[i]); g != seedGPS {
			t.Errorf("pt %d: GPS=%.6f want frozen=%.6f (chunk seed pt %d)",
				i, g, seedGPS, chunkSeedIdx(i, chunkSize60k))
		}
		if i > 0 && pt14Z(sel[i]) != pt14Z(sel[0]) {
			zDiffers = true
		}
	}
	if !zDiffers {
		t.Error("Z never changed — test data may be degenerate")
	}
}

// ---------------------------------------------------------------------------
// Test: selective + seek — seek should produce correct results even with
// partial decompression (skipped layers are replayed identically on seek).
// ---------------------------------------------------------------------------

func TestSelectiveDecompress_WithSeek(t *testing.T) {
	path := filepath.Join("", selTestFile60k)

	// Sequential ground truth with only Z requested.
	sel := openSelective(t, path, LASZIP_DECOMPRESS_SELECTIVE_Z)

	// Open with the same mask, use Seek to jump around.
	u, err := OpenLASSelective(path, LASZIP_DECOMPRESS_SELECTIVE_Z)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	for _, idx := range []uint32{0, 50, 100, 500, 999} {
		got := seekReadOne(t, u, idx)
		if pt14Z(got) != pt14Z(sel[idx]) {
			t.Errorf("selective+seek pt %d: Z got %d want %d",
				idx, pt14Z(got), pt14Z(sel[idx]))
		}
	}
}
