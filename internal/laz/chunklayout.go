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

// chunklayout.go — chunk-level access on top of LASreadPoint. Not part of the
// C++ port: LASzip only reads a file front to back (or seeks), while a chunk
// is a self-contained unit that can be decoded on its own, and therefore on
// several goroutines at once.
package laz

import (
	"errors"
	"fmt"
	"io"
	"math"
)

// ErrNoChunkLayout reports that a point stream cannot be split into
// independently decodable chunks: it is compressed without chunking, or its
// chunk table is missing, corrupt or inconsistent with the point count.
var ErrNoChunkLayout = errors.New("no usable chunk layout")

// ChunkSpan locates one chunk: an independently decodable run of points.
type ChunkSpan struct {
	Offset     int64  // absolute position of the chunk's first byte
	Size       int64  // length of the chunk in bytes
	FirstPoint uint64 // index of the chunk's first point
	NumPoints  uint32 // number of points in the chunk
}

// ReadChunkLayout returns the chunks of a compressed point stream, from its
// chunk table. src must be positioned anywhere; pointDataOffset is where the
// point data starts (the 8-byte chunk table pointer). The table is read with
// the same code, and the same tolerance checks, as sequential reading; any
// condition under which sequential reading would fall back to "no chunk
// table" yields ErrNoChunkLayout here.
func ReadChunkLayout(src io.ReadSeeker, pointDataOffset int64, items []LASitem, lz *LASzip, numPoints uint64) ([]ChunkSpan, error) {
	if lz == nil || lz.Compressor == LASZIP_COMPRESSOR_NONE {
		return nil, fmt.Errorf("%w: stream is not compressed", ErrNoChunkLayout)
	}
	if lz.Compressor == LASZIP_COMPRESSOR_POINTWISE {
		return nil, fmt.Errorf("%w: pointwise compression has no chunks", ErrNoChunkLayout)
	}
	if numPoints == 0 {
		return nil, nil
	}

	rp := NewLASreadPoint(0)
	if err := rp.Setup(uint32(len(items)), items, lz); err != nil {
		return nil, err
	}
	stream := NewByteStreamInReader(src)
	if err := stream.Seek(pointDataOffset); err != nil {
		return nil, err
	}
	if err := rp.Init(stream); err != nil {
		return nil, err
	}
	if err := rp.initDec(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoChunkLayout, err)
	}
	if rp.lastWarning != "" {
		return nil, fmt.Errorf("%w: %s", ErrNoChunkLayout, rp.lastWarning)
	}
	n := rp.numberChunks
	if n == 0 || n == math.MaxUint32 || rp.tabledChunks != n+1 || uint64(len(rp.chunkStarts)) != uint64(n)+1 {
		return nil, fmt.Errorf("%w: chunk table is incomplete", ErrNoChunkLayout)
	}

	spans := make([]ChunkSpan, n)
	first := uint64(0)
	for i := uint32(0); i < n; i++ {
		var count uint64
		switch {
		case rp.chunkTotals != nil: // adaptive chunking: counts come from the table
			count = uint64(rp.chunkTotals[i+1] - rp.chunkTotals[i])
		case i+1 < n:
			count = uint64(lz.ChunkSize)
		default:
			if numPoints <= first {
				return nil, fmt.Errorf("%w: chunk table has more chunks than the point count allows", ErrNoChunkLayout)
			}
			count = numPoints - first
			if count > uint64(lz.ChunkSize) {
				return nil, fmt.Errorf("%w: chunk table has fewer chunks than the point count needs", ErrNoChunkLayout)
			}
		}
		size := rp.chunkStarts[i+1] - rp.chunkStarts[i]
		if count == 0 || count > math.MaxUint32 || size <= 0 {
			return nil, fmt.Errorf("%w: chunk %d is empty or malformed", ErrNoChunkLayout, i)
		}
		spans[i] = ChunkSpan{Offset: rp.chunkStarts[i], Size: size, FirstPoint: first, NumPoints: uint32(count)}
		first += count
	}
	if first != numPoints {
		return nil, fmt.Errorf("%w: chunk table covers %d points, header declares %d", ErrNoChunkLayout, first, numPoints)
	}
	return spans, nil
}

// InitChunk binds rp to a stream positioned on the first byte of one chunk,
// as located by ReadChunkLayout (or, for uncompressed data, on the first byte
// of any point record). The following Read calls decode that chunk's points
// in order; the caller counts them and must not read past the chunk's last
// point. No chunk table is consulted, so rp can decode any chunk of a file,
// in any order, independently of every other LASreadPoint.
func (rp *LASreadPoint) InitChunk(instream ByteStreamIn) error {
	if instream == nil {
		return fmt.Errorf("nil instream")
	}
	rp.instream = instream
	for i := range rp.readersRaw {
		if err := rp.readersRaw[i].Init(instream); err != nil {
			return err
		}
	}
	if rp.dec == nil {
		rp.readers = rp.rawItems
		return nil
	}
	rp.dec.Done()
	// readers == nil makes the next Read take the first-point-of-chunk path.
	// chunkCount can never reach chunkSize, so the chunk-boundary logic (and
	// with it the chunk table) stays out of the way.
	rp.readers = nil
	rp.chunkSize = math.MaxUint32
	rp.chunkCount = 0
	rp.currentChunk = 0
	rp.tabledChunks = 0
	rp.numberChunks = 0
	rp.chunkStarts = nil
	rp.chunkTotals = nil
	return nil
}
