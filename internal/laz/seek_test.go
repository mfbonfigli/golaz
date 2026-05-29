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
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// readAllBytes reads every point from path and returns a slice of flat byte
// buffers, one per point. The unzipper is closed before returning.
func readAllBytes(t *testing.T, path string) [][]byte {
	t.Helper()
	u, err := OpenLAS(path)
	if err != nil {
		t.Fatalf("OpenLAS %q: %v", path, err)
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
			t.Fatalf("sequential read pt %d: %v", i, err)
		}
		out[i] = buf
	}
	return out
}

// seekReadOne opens path, seeks to idx, reads one point and returns its bytes.
func seekReadOne(t *testing.T, u *LASunzipper, idx uint32) []byte {
	t.Helper()
	items := u.Items()
	offsets := u.Offsets()
	total := offsets[len(offsets)-1]

	if err := u.Seek(idx); err != nil {
		t.Fatalf("Seek(%d): %v", idx, err)
	}
	buf := make([]byte, total)
	pt := make([][]byte, len(items))
	for j := range items {
		pt[j] = buf[offsets[j]:offsets[j+1]]
	}
	if err := u.Read(pt); err != nil {
		t.Fatalf("Read after Seek(%d): %v", idx, err)
	}
	return buf
}

func expectBytes(t *testing.T, label string, got, want []byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: len got=%d want=%d", label, len(got), len(want))
		return
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("%s: byte[%d] got 0x%02x want 0x%02x", label, i, g, want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: LAS 1.4 LAZ (LAYERED_CHUNKED — has chunk table, ten chunks)
// File: las14_pf6_1000pts_with_extrabytes.laz  (chunk_size=100)
//   chunk 0: pts 0..99
//   chunk 1: pts 100..199
//   ...
//   chunk 9: pts 900..999
// ---------------------------------------------------------------------------

func TestSeek_LAZ14_ForwardWithinChunk(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	for _, idx := range []uint32{0, 1, 50, 99, 100, 999} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" fwd pt"+itoa(idx), got, truth[idx])
	}
}

func TestSeek_LAZ14_BackwardWithinChunk(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// Read forward to 150 (covering chunk 0 and chunk 1), then seek backward.
	for range uint32(150) {
		items := u.Items()
		offsets := u.Offsets()
		buf := make([]byte, offsets[len(offsets)-1])
		pt := make([][]byte, len(items))
		for j := range items {
			pt[j] = buf[offsets[j]:offsets[j+1]]
		}
		u.Read(pt) //nolint
	}

	for _, idx := range []uint32{0, 1, 50, 99, 149} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" bwd pt"+itoa(idx), got, truth[idx])
	}
}

func TestSeek_LAZ14_CrossChunkForward(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// Seek directly into later chunks from the start.
	for _, idx := range []uint32{100, 101, 500, 999} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" chunk1 pt"+itoa(idx), got, truth[idx])
	}
}

func TestSeek_LAZ14_CrossChunkBackward(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// Read into a later chunk, then seek back to an earlier chunk.
	got := seekReadOne(t, u, 500)
	expectBytes(t, "reach chunk5", got, truth[500])

	got = seekReadOne(t, u, 50)
	expectBytes(t, "back to chunk0", got, truth[50])

	got = seekReadOne(t, u, 500)
	expectBytes(t, "back to chunk5 again", got, truth[500])
}

func TestSeek_LAZ14_ToZero(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// Read well into the file, seek to 0, read a run of points.
	seekReadOne(t, u, 500)

	if err := u.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	items := u.Items()
	offsets := u.Offsets()
	for i := range uint32(10) {
		buf := make([]byte, offsets[len(offsets)-1])
		pt := make([][]byte, len(items))
		for j := range items {
			pt[j] = buf[offsets[j]:offsets[j+1]]
		}
		if err := u.Read(pt); err != nil {
			t.Fatalf("read pt %d after Seek(0): %v", i, err)
		}
		expectBytes(t, "post-zero-seek pt"+itoa(i), buf, truth[i])
	}
}

// ---------------------------------------------------------------------------
// Tests: LAS 1.4 uncompressed (raw byte arithmetic, no chunk table)
// ---------------------------------------------------------------------------

func TestSeek_LAS14_Uncompressed(t *testing.T) {
	const path = "testdata/las/las14_pf6_1000pts_with_extrabytes.las"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	for _, idx := range []uint32{0, 1, 100, 500, 999} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" pt"+itoa(idx), got, truth[idx])
	}

	// backward
	got := seekReadOne(t, u, 999)
	expectBytes(t, "last pt", got, truth[999])
	got = seekReadOne(t, u, 0)
	expectBytes(t, "back to first", got, truth[0])
}

// ---------------------------------------------------------------------------
// Tests: LAS 1.2 LAZ (POINTWISE compressor — single large chunk)
// ---------------------------------------------------------------------------

func TestSeek_LAZ12_Pointwise(t *testing.T) {
	const path = "testdata/las/las12_pf0_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	// Forward seeks
	for _, idx := range []uint32{0, 1, 100, 500, 999} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" fwd pt"+itoa(idx), got, truth[idx])
	}

	// Backward seek: from 999 back to 100
	got := seekReadOne(t, u, 100)
	expectBytes(t, "bwd to 100", got, truth[100])
}

// ---------------------------------------------------------------------------
// Tests: LAS 1.3 LAZ (POINTWISE, GPS time, pf1)
// ---------------------------------------------------------------------------

func TestSeek_LAZ13_Pointwise(t *testing.T) {
	const path = "testdata/las/las13_pf1_1000pts_with_extrabytes.laz"
	truth := readAllBytes(t, filepath.Join("", path))

	u, err := OpenLAS(path)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()

	for _, idx := range []uint32{0, 100, 500, 999} {
		got := seekReadOne(t, u, idx)
		expectBytes(t, filepath.Base(path)+" pt"+itoa(idx), got, truth[idx])
	}

	got := seekReadOne(t, u, 0)
	expectBytes(t, "bwd to 0", got, truth[0])
}

// ---------------------------------------------------------------------------
// small helper: uint32 to string without importing strconv in test
// ---------------------------------------------------------------------------

func itoa(n uint32) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
