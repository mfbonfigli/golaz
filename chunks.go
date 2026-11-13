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
	"errors"
	"fmt"
	"io"

	laz "github.com/mfbonfigli/golaz/internal/laz"
)

// ---------------------------------------------------------------------------
// Chunk-level decoding — the building block for reading one file in parallel
// ---------------------------------------------------------------------------

// ErrChunksUnavailable is returned by Reader.Chunks and Reader.NewChunkDecoder
// when a file cannot be decoded chunk by chunk. The file is still readable
// front to back with Scan. Causes:
//
//   - the source does not implement io.ReaderAt (only possible with OpenReader);
//   - the file uses the legacy pointwise compressor, which has no chunks;
//   - the chunk table is missing, corrupt, or disagrees with the point count.
//
// Test for it with errors.Is; the wrapped message names the cause.
var ErrChunksUnavailable = errors.New("golaz: chunk-level decoding is not available for this file")

// uncompressedChunkPoints is the size of the chunks an uncompressed LAS file
// is presented as. Any point of an uncompressed file can be decoded on its
// own, so the split is arbitrary; this matches the usual LAZ chunk size.
// A variable so tests can exercise several chunks on small files.
var uncompressedChunkPoints uint32 = 50000

// Chunk describes one independently decodable run of consecutive points.
type Chunk struct {
	// FirstPoint is the index of the chunk's first point: the value Tell
	// reports before that point is read sequentially.
	FirstPoint uint64
	// NumPoints is the number of points in the chunk.
	NumPoints uint32

	offset int64 // absolute file position of the chunk's first byte
	size   int64 // length in bytes
}

// Chunks returns the file's chunks in file order. Together they cover every
// point exactly once: chunk i+1 starts where chunk i ends, and the point
// counts add up to NumPoints.
//
// For LAZ files these are the compressed chunks listed in the chunk table
// (50 000 points each by default, or variable-size). An uncompressed LAS file
// is presented as fixed runs of 50 000 points.
//
// The layout is computed on the first call and cached. Chunks is safe for
// concurrent use and does not move the Reader's read position. The returned
// slice is owned by the Reader; do not modify it.
//
// It returns an error wrapping ErrChunksUnavailable when the file has to be
// read sequentially.
func (r *Reader) Chunks() ([]Chunk, error) {
	r.chunksOnce.Do(func() {
		r.chunks, r.chunksErr = r.loadChunks()
	})
	return r.chunks, r.chunksErr
}

func (r *Reader) loadChunks() ([]Chunk, error) {
	if r.ra == nil {
		return nil, fmt.Errorf("%w: the source does not implement io.ReaderAt", ErrChunksUnavailable)
	}
	dataStart := int64(r.header.OffsetToPointData)

	if r.lz == nil {
		// Uncompressed: fixed-size records, split into arbitrary runs.
		recLen := int64(r.header.PointDataRecordLength)
		if dataStart+int64(r.totalPoints)*recLen > r.size {
			return nil, fmt.Errorf("%w: the file is shorter than its %d points", ErrChunksUnavailable, r.totalPoints)
		}
		per := uint64(uncompressedChunkPoints)
		chunks := make([]Chunk, 0, (r.totalPoints+per-1)/per)
		for first := uint64(0); first < r.totalPoints; first += per {
			n := min(per, r.totalPoints-first)
			chunks = append(chunks, Chunk{
				FirstPoint: first,
				NumPoints:  uint32(n),
				offset:     dataStart + int64(first)*recLen,
				size:       int64(n) * recLen,
			})
		}
		return chunks, nil
	}

	spans, err := laz.ReadChunkLayout(io.NewSectionReader(r.ra, 0, r.size), dataStart, r.items, r.lz, r.totalPoints)
	if err != nil {
		if errors.Is(err, laz.ErrNoChunkLayout) {
			return nil, fmt.Errorf("%w: %v", ErrChunksUnavailable, err)
		}
		return nil, fmt.Errorf("read chunk table: %w", err)
	}
	chunks := make([]Chunk, len(spans))
	for i, s := range spans {
		if s.Offset < dataStart || s.Offset+s.Size > r.size {
			return nil, fmt.Errorf("%w: chunk %d lies outside the file", ErrChunksUnavailable, i)
		}
		chunks[i] = Chunk{FirstPoint: s.FirstPoint, NumPoints: s.NumPoints, offset: s.Offset, size: s.Size}
	}
	return chunks, nil
}

// ChunkDecoder decodes whole chunks of the Reader it was created from. It
// has its own decode state and buffers, so several ChunkDecoders can run at
// the same time, one per goroutine, on the same Reader; a single
// ChunkDecoder is not safe for concurrent use.
//
// A ChunkDecoder honours the options the Reader was opened with (selective
// decompression mask, compatibility mode) and yields exactly the points
// Scan yields for the same indices. It reads the file through io.ReaderAt,
// so it neither depends on nor changes the Reader's own read position, and
// it stays valid until the Reader is closed.
//
// Typical use, one decoder per goroutine:
//
//	chunks, err := r.Chunks()
//	...
//	d, err := r.NewChunkDecoder()
//	...
//	var p golaz.Point
//	for i := range chunks { // each goroutine takes its own share of i
//		if err := d.Load(i); err != nil { ... }
//		for {
//			err := d.Scan(&p)
//			if err == io.EOF {
//				break
//			}
//			...
//		}
//	}
type ChunkDecoder struct {
	r      *Reader
	chunks []Chunk

	lp     *laz.LASreadPoint
	stream *laz.ByteStreamInArray

	data      []byte   // bytes of the loaded chunk; reused across Load calls
	ptBuf     [][]byte // per-item slices into flatBuf
	flatBuf   []byte   // one point in in-memory layout
	onDiskBuf []byte   // one point in on-disk layout (pf6-10 Raw)

	chunk     int    // index of the loaded chunk, -1 when none is loaded
	remaining uint32 // points of the loaded chunk not yet scanned
}

// NewChunkDecoder returns a decoder for the Reader's chunks. It is safe for
// concurrent use. It returns an error wrapping ErrChunksUnavailable when the
// file has to be read sequentially (see Chunks).
func (r *Reader) NewChunkDecoder() (*ChunkDecoder, error) {
	chunks, err := r.Chunks()
	if err != nil {
		return nil, err
	}
	n := uint32(len(r.items))
	lp := laz.NewLASreadPoint(r.selective)
	if err := lp.Setup(n, r.items, r.lzCfg); err != nil {
		return nil, fmt.Errorf("setup chunk decoder: %w", err)
	}
	d := &ChunkDecoder{
		r:         r,
		chunks:    chunks,
		lp:        lp,
		stream:    laz.NewByteStreamInArray(nil),
		flatBuf:   make([]byte, r.offsets[n]),
		ptBuf:     make([][]byte, n),
		onDiskBuf: make([]byte, r.header.PointDataRecordLength),
		chunk:     -1,
	}
	for i := uint32(0); i < n; i++ {
		d.ptBuf[i] = d.flatBuf[r.offsets[i]:r.offsets[i+1]]
	}
	return d, nil
}

// Load reads chunk i (an index into Reader.Chunks) into memory and positions
// the decoder on its first point. Chunks can be loaded in any order and any
// number of times.
func (d *ChunkDecoder) Load(i int) error {
	d.remaining = 0
	d.chunk = -1
	if i < 0 || i >= len(d.chunks) {
		return fmt.Errorf("load chunk %d: file has %d chunks", i, len(d.chunks))
	}
	c := d.chunks[i]
	if int64(cap(d.data)) < c.size {
		d.data = make([]byte, c.size)
	}
	d.data = d.data[:c.size]
	if _, err := d.r.ra.ReadAt(d.data, c.offset); err != nil {
		return fmt.Errorf("load chunk %d: %w", i, err)
	}
	d.stream.Init(d.data)
	if err := d.lp.InitChunk(d.stream); err != nil {
		return fmt.Errorf("load chunk %d: %w", i, err)
	}
	d.chunk = i
	d.remaining = c.NumPoints
	return nil
}

// Scan decodes the next point of the loaded chunk into p. It returns io.EOF
// after the chunk's last point, and when no chunk is loaded.
// Like Reader.Scan it does not allocate, and the point's Raw and ExtraBytes
// slices share the decoder's buffers: they are valid until the next Scan or
// Load on this decoder.
//
// A decode error means the chunk is corrupt: the rest of that chunk cannot
// be recovered and further Scan calls return io.EOF, but other chunks are
// unaffected.
func (d *ChunkDecoder) Scan(p *Point) error {
	if d.remaining == 0 {
		return io.EOF
	}
	if err := d.lp.Read(d.ptBuf); err != nil {
		c := d.chunks[d.chunk]
		done := c.NumPoints - d.remaining
		d.remaining = 0
		return fmt.Errorf("chunk %d: read point %d: %w", d.chunk, c.FirstPoint+uint64(done), err)
	}
	d.remaining--

	r := d.r
	populatePoint(
		p,
		r.diskFormat,
		r.present,
		d.flatBuf,
		d.onDiskBuf,
		r.items,
		r.offsets,
		r.scaleX, r.scaleY, r.scaleZ,
		r.offsetX, r.offsetY, r.offsetZ,
	)
	if r.compat != nil {
		reconstructCompat(p, r.compat)
	}
	return nil
}

// Remaining returns the number of points of the loaded chunk that Scan has
// not returned yet.
func (d *ChunkDecoder) Remaining() int { return int(d.remaining) }
