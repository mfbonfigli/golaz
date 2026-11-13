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

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// lasFixtures returns every .las/.laz file directly inside dir.
func lasFixtures(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (!strings.HasSuffix(name, ".las") && !strings.HasSuffix(name, ".laz")) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

// pointRecord is everything a Point exposes, copied out of the shared buffers.
type pointRecord struct {
	p     Point // exported fields + unexported optional fields (compared field by field)
	raw   []byte
	extra []byte
}

func recordOf(p *Point) pointRecord {
	rec := pointRecord{p: *p}
	rec.raw = append([]byte(nil), p.Raw()...)
	rec.extra = append([]byte(nil), p.ExtraBytes()...)
	rec.p.raw, rec.p.extraBuf = nil, nil
	return rec
}

func (a pointRecord) equal(b pointRecord) bool {
	x, y := a.p, b.p
	// The comparison is bitwise on purpose: decoding a chunk on its own must
	// reproduce the sequential result exactly, NaN payloads included.
	return x.RawX == y.RawX && x.RawY == y.RawY && x.RawZ == y.RawZ &&
		x.X == y.X && x.Y == y.Y && x.Z == y.Z &&
		x.Intensity == y.Intensity &&
		x.ReturnNumber == y.ReturnNumber && x.NumberOfReturns == y.NumberOfReturns &&
		x.ScanDirectionFlag == y.ScanDirectionFlag && x.EdgeOfFlightLine == y.EdgeOfFlightLine &&
		x.Classification == y.Classification && x.ClassificationFlags == y.ClassificationFlags &&
		x.UserData == y.UserData && x.PointSourceID == y.PointSourceID &&
		x.ScanAngleDegrees == y.ScanAngleDegrees &&
		x.format == y.format && x.present == y.present &&
		x.gpsTime == y.gpsTime &&
		x.red == y.red && x.green == y.green && x.blue == y.blue && x.nir == y.nir &&
		x.scannerChannel == y.scannerChannel &&
		x.waveIdx == y.waveIdx && x.waveOffset == y.waveOffset && x.waveSize == y.waveSize &&
		x.waveLoc == y.waveLoc && x.waveXt == y.waveXt && x.waveYt == y.waveYt && x.waveZt == y.waveZt &&
		bytes.Equal(a.raw, b.raw) && bytes.Equal(a.extra, b.extra)
}

// scanSequential reads the whole file with Reader.Scan.
func scanSequential(t *testing.T, r *Reader) []pointRecord {
	t.Helper()
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	recs := make([]pointRecord, 0, r.NumPoints())
	var p Point
	for {
		err := r.Scan(&p)
		if err == io.EOF {
			return recs
		}
		if err != nil {
			t.Fatalf("Scan %d: %v", len(recs), err)
		}
		recs = append(recs, recordOf(&p))
	}
}

// decodeChunk returns the points of chunk i.
func decodeChunk(d *ChunkDecoder, i int) ([]pointRecord, error) {
	if err := d.Load(i); err != nil {
		return nil, err
	}
	recs := make([]pointRecord, 0, d.Remaining())
	var p Point
	for {
		err := d.Scan(&p)
		if err == io.EOF {
			return recs, nil
		}
		if err != nil {
			return recs, err
		}
		recs = append(recs, recordOf(&p))
	}
}

// checkChunksCoverFile asserts the layout invariants documented on Chunks.
func checkChunksCoverFile(t *testing.T, r *Reader, chunks []Chunk) {
	t.Helper()
	next := uint64(0)
	for i, c := range chunks {
		if c.FirstPoint != next {
			t.Fatalf("chunk %d starts at point %d, want %d", i, c.FirstPoint, next)
		}
		if c.NumPoints == 0 {
			t.Fatalf("chunk %d is empty", i)
		}
		next += uint64(c.NumPoints)
	}
	if next != r.NumPoints() {
		t.Fatalf("chunks cover %d points, file has %d", next, r.NumPoints())
	}
}

// checkChunkedEqualsSequential decodes every chunk, in reverse order to prove
// independence, and compares with sequential reading.
func checkChunkedEqualsSequential(t *testing.T, r *Reader) {
	t.Helper()
	want := scanSequential(t, r)
	chunks, err := r.Chunks()
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	checkChunksCoverFile(t, r, chunks)
	d, err := r.NewChunkDecoder()
	if err != nil {
		t.Fatalf("NewChunkDecoder: %v", err)
	}
	for i := len(chunks) - 1; i >= 0; i-- {
		got, err := decodeChunk(d, i)
		if err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		c := chunks[i]
		if len(got) != int(c.NumPoints) {
			t.Fatalf("chunk %d: decoded %d points, want %d", i, len(got), c.NumPoints)
		}
		for k := range got {
			if !got[k].equal(want[c.FirstPoint+uint64(k)]) {
				t.Fatalf("chunk %d: point %d differs from sequential read:\n got %+v\nwant %+v",
					i, c.FirstPoint+uint64(k), got[k], want[c.FirstPoint+uint64(k)])
			}
		}
	}
}

// withSmallUncompressedChunks makes uncompressed files split into several
// chunks even though the fixtures hold at most 1000 points.
func withSmallUncompressedChunks(t *testing.T) {
	t.Helper()
	old := uncompressedChunkPoints
	uncompressedChunkPoints = 64
	t.Cleanup(func() { uncompressedChunkPoints = old })
}

// Every fixture either decodes chunk by chunk to exactly the sequential
// result, or reports ErrChunksUnavailable.
func TestChunkDecoder_MatchesSequential_AllFixtures(t *testing.T) {
	withSmallUncompressedChunks(t)
	files := append(lasFixtures(t, tdDir), lasFixtures(t, oracleDir)...)
	files = append(files, lasFixtures(t, filepath.Join(oracleDir, "compat"))...)
	decoded := 0
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			r, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()
			if _, err := r.Chunks(); err != nil {
				if !errors.Is(err, ErrChunksUnavailable) {
					t.Fatalf("Chunks: unexpected error %v", err)
				}
				// Only the legacy pointwise (non-chunked) fixture may opt out.
				if !strings.Contains(path, "_v1pw.laz") {
					t.Fatalf("Chunks unavailable for a chunked file: %v", err)
				}
				if _, err := r.NewChunkDecoder(); !errors.Is(err, ErrChunksUnavailable) {
					t.Fatalf("NewChunkDecoder: got %v, want ErrChunksUnavailable", err)
				}
				return
			}
			checkChunkedEqualsSequential(t, r)
			decoded++
		})
	}
	if decoded < 100 {
		t.Errorf("only %d fixtures were decoded chunk by chunk", decoded)
	}
}

// The chunk layout of a LAZ file follows its chunk table: the 1000-point
// fixtures are written with 100-point chunks.
func TestChunks_LAZLayout(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	chunks, err := r.Chunks()
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 10 {
		t.Fatalf("got %d chunks, want 10", len(chunks))
	}
	for i, c := range chunks {
		if c.FirstPoint != uint64(i)*100 || c.NumPoints != 100 {
			t.Errorf("chunk %d = {FirstPoint %d, NumPoints %d}, want {%d, 100}", i, c.FirstPoint, c.NumPoints, i*100)
		}
	}
}

// An uncompressed file is split into fixed runs, the last one shorter.
func TestChunks_UncompressedLayout(t *testing.T) {
	withSmallUncompressedChunks(t)
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.las")
	defer r.Close()
	chunks, err := r.Chunks()
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 16 { // 15 * 64 + 40
		t.Fatalf("got %d chunks, want 16", len(chunks))
	}
	if last := chunks[15]; last.FirstPoint != 960 || last.NumPoints != 40 {
		t.Errorf("last chunk = {FirstPoint %d, NumPoints %d}, want {960, 40}", last.FirstPoint, last.NumPoints)
	}
}

// Decoders on several goroutines, sharing one Reader, while the Reader itself
// is being scanned. Run with -race.
func TestChunkDecoder_Parallel(t *testing.T) {
	withSmallUncompressedChunks(t)
	for _, name := range []string{
		"las12_pf3_1000pts_with_extrabytes.laz", // v2 pointwise-chunked
		"las14_pf8_1000pts_with_extrabytes.laz", // v3 layered
		"las14_pf7_1000pts_with_extrabytes.las", // uncompressed
	} {
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			want := scanSequential(t, r)
			if err := r.Reset(); err != nil {
				t.Fatal(err)
			}
			chunks, err := r.Chunks()
			if err != nil {
				t.Fatal(err)
			}

			const workers = 4
			got := make([][]pointRecord, len(chunks))
			errs := make([]error, workers)
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					d, err := r.NewChunkDecoder() // concurrent creation is allowed
					if err != nil {
						errs[w] = err
						return
					}
					for i := w; i < len(chunks); i += workers {
						if got[i], err = decodeChunk(d, i); err != nil {
							errs[w] = err
							return
						}
					}
				}()
			}
			// Sequential reading on the same Reader goes on undisturbed.
			var p Point
			for i := range want {
				if err := r.Scan(&p); err != nil {
					t.Fatalf("Scan %d during parallel decode: %v", i, err)
				}
				if !recordOf(&p).equal(want[i]) {
					t.Fatalf("Scan %d changed while chunk decoders were running", i)
				}
			}
			wg.Wait()
			for w, err := range errs {
				if err != nil {
					t.Fatalf("worker %d: %v", w, err)
				}
			}
			n := 0
			for i, recs := range got {
				for k := range recs {
					if !recs[k].equal(want[chunks[i].FirstPoint+uint64(k)]) {
						t.Fatalf("chunk %d point %d differs from sequential read", i, k)
					}
				}
				n += len(recs)
			}
			if n != len(want) {
				t.Fatalf("decoded %d points in parallel, want %d", n, len(want))
			}
		})
	}
}

// The selective mask the Reader was opened with applies to its decoders.
func TestChunkDecoder_HonoursSelectiveMask(t *testing.T) {
	path := filepath.Join(tdDir, "las14_pf8_1000pts_with_extrabytes.laz")
	full, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	sel, err := Open(path, WithSelectiveMask(SelectiveZ))
	if err != nil {
		t.Fatal(err)
	}
	defer sel.Close()

	// Same as sequential reading with the same mask…
	checkChunkedEqualsSequential(t, sel)

	// …and really selective: intensity is frozen within a chunk, unlike in
	// the fully decoded file.
	fullRecs := scanSequential(t, full)
	d, err := sel.NewChunkDecoder()
	if err != nil {
		t.Fatal(err)
	}
	recs, err := decodeChunk(d, 3)
	if err != nil {
		t.Fatal(err)
	}
	frozen, variesInFull := true, false
	for k := range recs {
		if recs[k].p.Intensity != recs[0].p.Intensity {
			frozen = false
		}
		if fullRecs[300+k].p.Intensity != fullRecs[300].p.Intensity {
			variesInFull = true
		}
		if recs[k].p.Z != fullRecs[300+k].p.Z {
			t.Fatalf("point %d: Z differs from the fully decoded file", 300+k)
		}
	}
	if !variesInFull {
		t.Fatal("fixture has constant intensity in chunk 3: the test proves nothing")
	}
	if !frozen {
		t.Error("intensity layer was decoded although the mask excludes it")
	}
}

// readSeekerOnly hides ReadAt.
type readSeekerOnly struct{ rs io.ReadSeeker }

func (r readSeekerOnly) Read(p []byte) (int, error)                { return r.rs.Read(p) }
func (r readSeekerOnly) Seek(off int64, whence int) (int64, error) { return r.rs.Seek(off, whence) }

func TestChunks_OpenReader(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(tdDir, "las14_pf6_1000pts_with_extrabytes.laz"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("bytes.Reader supports chunks", func(t *testing.T) {
		r, err := OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		checkChunkedEqualsSequential(t, r)
	})

	t.Run("source without ReadAt", func(t *testing.T) {
		r, err := OpenReader(readSeekerOnly{bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if _, err := r.Chunks(); !errors.Is(err, ErrChunksUnavailable) {
			t.Errorf("Chunks: got %v, want ErrChunksUnavailable", err)
		}
		if _, err := r.NewChunkDecoder(); !errors.Is(err, ErrChunksUnavailable) {
			t.Errorf("NewChunkDecoder: got %v, want ErrChunksUnavailable", err)
		}
		// Sequential reading still works.
		if got := len(scanSequential(t, r)); got != 1000 {
			t.Errorf("sequential read returned %d points, want 1000", got)
		}
	})
}

func TestChunkDecoder_LoadAndScanContract(t *testing.T) {
	r := openReader(t, "las14_pf6_1000pts_with_extrabytes.laz")
	defer r.Close()
	d, err := r.NewChunkDecoder()
	if err != nil {
		t.Fatal(err)
	}
	var p Point
	if err := d.Scan(&p); err != io.EOF {
		t.Errorf("Scan before Load: got %v, want io.EOF", err)
	}
	for _, bad := range []int{-1, 10, 1 << 20} {
		if err := d.Load(bad); err == nil {
			t.Errorf("Load(%d): no error", bad)
		}
		if err := d.Scan(&p); err != io.EOF {
			t.Errorf("Scan after failed Load(%d): got %v, want io.EOF", bad, err)
		}
	}

	// A partly read chunk can be abandoned for another one, and reloaded.
	if err := d.Load(2); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if err := d.Scan(&p); err != nil {
			t.Fatal(err)
		}
	}
	if d.Remaining() != 93 {
		t.Errorf("Remaining = %d, want 93", d.Remaining())
	}
	first, err := decodeChunk(d, 5)
	if err != nil {
		t.Fatal(err)
	}
	again, err := decodeChunk(d, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 100 || len(again) != 100 {
		t.Fatalf("chunk 5 decoded to %d then %d points, want 100", len(first), len(again))
	}
	for k := range first {
		if !first[k].equal(again[k]) {
			t.Fatalf("chunk 5 point %d differs between two loads", k)
		}
	}
	if err := d.Scan(&p); err != io.EOF {
		t.Errorf("Scan past the end of a chunk: got %v, want io.EOF", err)
	}
}

// Files whose chunk table cannot be trusted must not be decoded by chunk; a
// file with one damaged chunk fails only in that chunk.
func TestChunks_CorruptFiles(t *testing.T) {
	dir := filepath.Join(oracleDir, "corrupt")
	for _, name := range []string{
		"chunked_tableoffset_minus1.laz",
		"chunked_tableoffset_garbage.laz",
		"chunked_truncated.laz",
	} {
		t.Run(name, func(t *testing.T) {
			r, err := Open(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, err := r.Chunks(); !errors.Is(err, ErrChunksUnavailable) {
				t.Errorf("Chunks: got %v, want ErrChunksUnavailable", err)
			}
		})
	}

	t.Run("chunked_midchunk_corrupt.laz", func(t *testing.T) {
		r, err := Open(filepath.Join(dir, "chunked_midchunk_corrupt.laz"))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		chunks, err := r.Chunks()
		if err != nil {
			t.Fatalf("Chunks: %v (the chunk table of this file is intact)", err)
		}
		checkChunksCoverFile(t, r, chunks)
		d, err := r.NewChunkDecoder()
		if err != nil {
			t.Fatal(err)
		}
		good, bad := 0, 0
		for i := range chunks {
			recs, err := decodeChunk(d, i)
			if err != nil {
				bad++
				continue
			}
			if len(recs) != int(chunks[i].NumPoints) {
				t.Errorf("chunk %d: %d points without error, want %d", i, len(recs), chunks[i].NumPoints)
			}
			good++
		}
		if bad == 0 {
			t.Error("no chunk reported the corruption")
		}
		if good == 0 {
			t.Error("corruption in one chunk made every chunk fail")
		}
		t.Logf("%d chunks decoded, %d reported corruption", good, bad)
	})
}

// Like Reader.Scan, ChunkDecoder.Scan must not allocate per point.
func TestChunkDecoder_AllocationsPerPoint(t *testing.T) {
	for _, name := range []string{
		"las14_pf7_1000pts_with_extrabytes.las",
		"las14_pf7_1000pts_with_extrabytes.laz",
		"las12_pf3_1000pts_with_extrabytes.laz",
	} {
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			chunks, err := r.Chunks()
			if err != nil {
				t.Fatal(err)
			}
			d, err := r.NewChunkDecoder()
			if err != nil {
				t.Fatal(err)
			}
			var p Point
			readAll := func() {
				for i := range chunks {
					if err := d.Load(i); err != nil {
						t.Fatal(err)
					}
					for d.Scan(&p) == nil {
					}
				}
			}
			readAll() // warm up
			perPoint := testing.AllocsPerRun(5, readAll) / float64(r.NumPoints())
			limit := 0.0
			if strings.HasSuffix(name, ".laz") {
				limit = 0.1 // a few per 100-point chunk
			}
			if perPoint > limit {
				t.Errorf("%.3f allocations per point, want at most %.3f", perPoint, limit)
			}
		})
	}
}
