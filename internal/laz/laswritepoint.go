// Copyright (c) 2007-2022 rapidlasso GmbH - fast tools to catch reality (Original C++ implementation)
// Copyright (c) 2026 Massimo Federico Bonfigli (Go port and modifications)
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
//
// This file is a Go port of LASzip (https://github.com/LASzip/LASzip).
// Changes: translated from C++ to Go.

// laswritepoint.go — LASwritePoint orchestrator, ported from
// src/laswritepoint.hpp/cpp. Manages raw and compressed writers for all
// point items, chunk boundaries, and chunk table emission. Mirror image of
// LASreadPoint in lasreadpoint.go.
package laz

import (
	"encoding/binary"
	"fmt"
	"math"
)

// LASwritePoint orchestrates writing LAS/LAZ points.
type LASwritePoint struct {
	outstream ByteStreamOut

	numWriters        uint32
	writers           []LASwriteItem           // active writers (nil until first point of a chunk when compressing)
	writersRaw        []LASwriteItem           // always populated
	writersCompressed []LASwriteItemCompressed // populated when compressing

	enc                     *ArithmeticEncoder
	layeredLAS14Compression bool

	// Chunking
	chunkSize               uint32
	chunkCount              uint32
	numberChunks            uint32
	chunkSizes              []uint32 // per-chunk point counts (variable-size chunking only)
	chunkBytes              []uint32 // per-chunk byte counts
	chunkTableStartPosition int64
	chunkStartPosition      int64
}

// NewLASwritePoint creates a new orchestrator (C++ constructor defaults).
func NewLASwritePoint() *LASwritePoint {
	return &LASwritePoint{
		chunkSize: math.MaxUint32,
	}
}

// Setup initialises the raw and (optionally) compressed writers based on
// the items described in lz. Must be called once before Init.
func (wp *LASwritePoint) Setup(numItems uint32, items []LASitem, lz *LASzip) error {
	// If laszip exists then we must use its items.
	if lz != nil {
		if numItems == 0 || items == nil {
			return fmt.Errorf("invalid input: numItems=%d, items=nil", numItems)
		}
		if numItems != uint32(lz.NumItems) {
			return fmt.Errorf("num_item mismatch: %d vs laszip %d", numItems, lz.NumItems)
		}
	}

	// --- entropy encoder ---------------------------------------------------
	wp.enc = nil
	wp.layeredLAS14Compression = false
	if lz != nil && lz.Compressor != LASZIP_COMPRESSOR_NONE {
		switch lz.Coder {
		case LASZIP_CODER_ARITHMETIC:
			wp.enc = NewArithmeticEncoder()
		default:
			return fmt.Errorf("coder %d not supported", lz.Coder)
		}
		wp.layeredLAS14Compression = (lz.Compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED)
	}

	// Initialize the writers.
	wp.writers = nil
	wp.numWriters = numItems

	// Disable chunking.
	wp.chunkSize = math.MaxUint32

	// --- raw writers (always created) --------------------------------------
	wp.writersRaw = make([]LASwriteItem, numItems)
	for i := range numItems {
		sz := uint32(items[i].Size)
		switch items[i].Type {
		case LASITEM_POINT10:
			wp.writersRaw[i] = &LASwriteItemRawPoint10LE{}
		case LASITEM_GPSTIME11:
			wp.writersRaw[i] = &LASwriteItemRawGpsTime11LE{}
		case LASITEM_RGB12, LASITEM_RGB14:
			wp.writersRaw[i] = &LASwriteItemRawRGB12LE{}
		case LASITEM_BYTE, LASITEM_BYTE14:
			wp.writersRaw[i] = NewLASwriteItemRawByte(sz)
		case LASITEM_POINT14:
			wp.writersRaw[i] = &LASwriteItemRawPoint14LE{}
		case LASITEM_RGBNIR14:
			wp.writersRaw[i] = &LASwriteItemRawRGBNIR14LE{}
		case LASITEM_WAVEPACKET13, LASITEM_WAVEPACKET14:
			wp.writersRaw[i] = &LASwriteItemRawWavepacket13LE{}
		default:
			return fmt.Errorf("item %d: unsupported type %d", i, items[i].Type)
		}
	}

	// --- compressed writers -------------------------------------------------
	if wp.enc != nil {
		wp.writersCompressed = make([]LASwriteItemCompressed, numItems)
		for i := range numItems {
			ver := items[i].Version
			sz := uint32(items[i].Size)
			switch items[i].Type {
			case LASITEM_POINT10:
				switch ver {
				case 2:
					wp.writersCompressed[i] = NewLASwriteItemCompressedPoint10v2(wp.enc)
				default:
					// v1 writers are deliberately not ported (nothing emits
					// v1 items unless explicitly requested).
					return fmt.Errorf("POINT10 version %d not supported for writing", ver)
				}
			case LASITEM_GPSTIME11:
				switch ver {
				case 2:
					wp.writersCompressed[i] = NewLASwriteItemCompressedGpsTime11v2(wp.enc)
				default:
					return fmt.Errorf("GPSTIME11 version %d not supported for writing", ver)
				}
			case LASITEM_RGB12:
				switch ver {
				case 2:
					wp.writersCompressed[i] = NewLASwriteItemCompressedRGB12v2(wp.enc)
				default:
					return fmt.Errorf("RGB12 version %d not supported for writing", ver)
				}
			case LASITEM_BYTE:
				switch ver {
				case 2:
					wp.writersCompressed[i] = NewLASwriteItemCompressedBytev2(wp.enc, sz)
				default:
					return fmt.Errorf("BYTE version %d not supported for writing", ver)
				}
			case LASITEM_WAVEPACKET13:
				switch ver {
				case 1:
					wp.writersCompressed[i] = NewLASwriteItemCompressedWavepacket13v1(wp.enc)
				default:
					return fmt.Errorf("WAVEPACKET13 version %d not supported for writing", ver)
				}
			case LASITEM_POINT14:
				switch ver {
				case 3:
					wp.writersCompressed[i] = NewLASwriteItemCompressedPoint14v3(wp.enc)
				case 4:
					wp.writersCompressed[i] = NewLASwriteItemCompressedPoint14v4(wp.enc)
				default:
					return fmt.Errorf("POINT14 version %d not supported for writing", ver)
				}
			case LASITEM_RGB14:
				switch ver {
				case 3:
					wp.writersCompressed[i] = NewLASwriteItemCompressedRGB14v3(wp.enc)
				case 4:
					wp.writersCompressed[i] = NewLASwriteItemCompressedRGB14v4(wp.enc)
				default:
					return fmt.Errorf("RGB14 version %d not supported for writing", ver)
				}
			case LASITEM_RGBNIR14:
				switch ver {
				case 3:
					wp.writersCompressed[i] = NewLASwriteItemCompressedRGBNIR14v3(wp.enc)
				case 4:
					wp.writersCompressed[i] = NewLASwriteItemCompressedRGBNIR14v4(wp.enc)
				default:
					return fmt.Errorf("RGBNIR14 version %d not supported for writing", ver)
				}
			case LASITEM_BYTE14:
				switch ver {
				case 3:
					wp.writersCompressed[i] = NewLASwriteItemCompressedByte14v3(wp.enc, sz)
				case 4:
					wp.writersCompressed[i] = NewLASwriteItemCompressedByte14v4(wp.enc, sz)
				default:
					return fmt.Errorf("BYTE14 version %d not supported for writing", ver)
				}
			case LASITEM_WAVEPACKET14:
				switch ver {
				case 3:
					wp.writersCompressed[i] = NewLASwriteItemCompressedWavepacket14v3(wp.enc)
				case 4:
					wp.writersCompressed[i] = NewLASwriteItemCompressedWavepacket14v4(wp.enc)
				default:
					return fmt.Errorf("WAVEPACKET14 version %d not supported for writing", ver)
				}
			default:
				return fmt.Errorf("item %d: unsupported type %d", i, items[i].Type)
			}
		}
		if lz.Compressor != LASZIP_COMPRESSOR_POINTWISE {
			if lz.ChunkSize != 0 {
				wp.chunkSize = lz.ChunkSize
			}
			wp.chunkCount = 0
			wp.numberChunks = math.MaxUint32
		}
	}
	return nil
}

// Init binds the byte stream, reserves the chunk-table offset slot (first
// call only), and initialises the raw writers. It is called again internally
// at every chunk boundary to re-arm the writers.
func (wp *LASwritePoint) Init(outstream ByteStreamOut) error {
	if outstream == nil {
		return fmt.Errorf("nil outstream")
	}
	wp.outstream = outstream

	// If chunking is enabled, the first Init reserves the 8-byte chunk-table
	// offset slot. Seekable streams write the slot's own position as the
	// placeholder (load-bearing: the reader's "interrupted compressor"
	// detection checks chunkTableStart+8 == chunksStart); non-seekable
	// streams write -1 and append the real position after the table.
	if wp.numberChunks == math.MaxUint32 {
		wp.numberChunks = 0
		if outstream.IsSeekable() {
			pos, err := outstream.Tell()
			if err != nil {
				return fmt.Errorf("init: tell: %w", err)
			}
			wp.chunkTableStartPosition = pos
		} else {
			wp.chunkTableStartPosition = -1
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(wp.chunkTableStartPosition))
		if err := outstream.Put64bitsLE(buf[:]); err != nil {
			return fmt.Errorf("init: reserve chunk table slot: %w", err)
		}
		pos, err := outstream.Tell()
		if err != nil {
			return fmt.Errorf("init: tell: %w", err)
		}
		wp.chunkStartPosition = pos
	}

	for i := uint32(0); i < wp.numWriters; i++ {
		if err := wp.writersRaw[i].(rawWriter).Init(outstream); err != nil {
			return err
		}
	}

	if wp.enc != nil {
		wp.writers = nil
	} else {
		wp.writers = wp.writersRaw
	}
	return nil
}

// closeChunk finalizes the current chunk's entropy coding: layered
// compressors emit the point count and per-item layer sizes/bytes,
// non-layered compressors flush the shared encoder.
func (wp *LASwritePoint) closeChunk() error {
	if wp.layeredLAS14Compression {
		// Write how many points are in the chunk.
		var buf [4]byte
		binary.LittleEndian.PutUint32(buf[:], wp.chunkCount)
		if err := wp.outstream.Put32bitsLE(buf[:]); err != nil {
			return fmt.Errorf("close chunk: point count: %w", err)
		}
		// Write all layers.
		for i := uint32(0); i < wp.numWriters; i++ {
			if err := wp.writersCompressed[i].ChunkSizes(); err != nil {
				return err
			}
		}
		for i := uint32(0); i < wp.numWriters; i++ {
			if err := wp.writersCompressed[i].ChunkBytes(); err != nil {
				return err
			}
		}
	} else {
		if _, err := wp.enc.Done(); err != nil {
			return err
		}
	}
	return nil
}

// Write compresses (or raw-writes) one point. point[i] holds the in-memory
// item buffer for item i (POINT14 items use the 40-byte expanded layout).
func (wp *LASwritePoint) Write(point [][]byte) error {
	context := uint32(0)

	if wp.chunkCount == wp.chunkSize {
		if wp.enc != nil {
			if err := wp.closeChunk(); err != nil {
				return err
			}
			if err := wp.addChunkToTable(); err != nil {
				return err
			}
			if err := wp.Init(wp.outstream); err != nil {
				return err
			}
		}
		// (enc == nil happens *only* for uncompressed LAS with over
		// U32_MAX points, mirroring the C++ assert.)
		wp.chunkCount = 0
	}
	wp.chunkCount++

	if wp.writers != nil {
		for i := uint32(0); i < wp.numWriters; i++ {
			if err := wp.writers[i].Write(point[i], &context); err != nil {
				return err
			}
		}
	} else {
		// First point of a chunk: write it raw to the main stream, then seed
		// each compressed writer from it (POINT14 sets context, downstream
		// items consume it), then hand the stream to the encoder — the raw
		// bytes must precede the encoder init.
		for i := uint32(0); i < wp.numWriters; i++ {
			if err := wp.writersRaw[i].Write(point[i], &context); err != nil {
				return err
			}
			if err := wp.writersCompressed[i].Init(point[i], &context); err != nil {
				return err
			}
		}
		wp.writers = make([]LASwriteItem, wp.numWriters)
		for i := uint32(0); i < wp.numWriters; i++ {
			wp.writers[i] = wp.writersCompressed[i]
		}
		if err := wp.enc.Init(wp.outstream); err != nil {
			return err
		}
	}
	return nil
}

// Chunk closes the current chunk at an explicit, caller-chosen boundary.
// Only valid with variable-size chunking (chunk size == U32_MAX).
func (wp *LASwritePoint) Chunk() error {
	if wp.chunkStartPosition == 0 || wp.chunkSize != math.MaxUint32 {
		return fmt.Errorf("explicit chunking requires an initialized variable-size chunk writer")
	}
	if err := wp.closeChunk(); err != nil {
		return err
	}
	if err := wp.addChunkToTable(); err != nil {
		return err
	}
	if err := wp.Init(wp.outstream); err != nil {
		return err
	}
	wp.chunkCount = 0
	return nil
}

// Done flushes the partial last chunk (if any), adds it to the chunk table,
// and writes the chunk table (patching the reserved offset slot on seekable
// streams, appending the table position on non-seekable ones).
func (wp *LASwritePoint) Done() error {
	if wp.enc != nil && wp.writers != nil {
		// writers == writers_compressed in the C++: at least one point was
		// written since the last chunk boundary.
		if err := wp.closeChunk(); err != nil {
			return err
		}
		if wp.chunkStartPosition != 0 {
			if wp.chunkCount > 0 {
				if err := wp.addChunkToTable(); err != nil {
					return err
				}
			}
			return wp.writeChunkTable()
		}
	} else if wp.writers == nil {
		// Compressing but no point was ever written (or Init was never
		// called): still emit the (empty) chunk table if the stream was
		// initialized.
		if wp.chunkStartPosition != 0 {
			return wp.writeChunkTable()
		}
	}
	return nil
}

// addChunkToTable records the byte size (and, for variable-size chunking,
// the point count) of the chunk that just ended.
func (wp *LASwritePoint) addChunkToTable() error {
	position, err := wp.outstream.Tell()
	if err != nil {
		return fmt.Errorf("add chunk to table: tell: %w", err)
	}
	if wp.chunkSize == math.MaxUint32 {
		wp.chunkSizes = append(wp.chunkSizes, wp.chunkCount)
	}
	wp.chunkBytes = append(wp.chunkBytes, uint32(position-wp.chunkStartPosition))
	wp.chunkStartPosition = position
	wp.numberChunks++
	return nil
}

// writeChunkTable emits the chunk table (see WRITER_PORT_PLAN.md §2.2):
//
//	U32 LE version (=0)
//	U32 LE number_chunks
//	[if >0] fresh enc.Init; IntegerCompressor(enc, 32, 2) InitCompressor;
//	  per chunk i:
//	    variable mode only: ic.Compress(prev_count, count[i], ctx 0)
//	    always:             ic.Compress(prev_bytes, bytes[i], ctx 1)
//	  enc.Done()
//	[non-seekable only] I64 LE chunk table position
//
// The values are per-chunk raw counts/bytes predicted by the previous
// chunk's value — NOT cumulative (the reader accumulates them).
func (wp *LASwritePoint) writeChunkTable() error {
	position, err := wp.outstream.Tell()
	if err != nil {
		return fmt.Errorf("write chunk table: tell: %w", err)
	}
	if wp.chunkTableStartPosition != -1 { // stream is seekable
		if err := wp.outstream.Seek(wp.chunkTableStartPosition); err != nil {
			return fmt.Errorf("write chunk table: seek to slot: %w", err)
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(position))
		if err := wp.outstream.Put64bitsLE(buf[:]); err != nil {
			return fmt.Errorf("write chunk table: patch slot: %w", err)
		}
		if err := wp.outstream.Seek(position); err != nil {
			return fmt.Errorf("write chunk table: seek back: %w", err)
		}
	}
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], 0) // version
	if err := wp.outstream.Put32bitsLE(buf[:]); err != nil {
		return fmt.Errorf("write chunk table: version: %w", err)
	}
	binary.LittleEndian.PutUint32(buf[:], wp.numberChunks)
	if err := wp.outstream.Put32bitsLE(buf[:]); err != nil {
		return fmt.Errorf("write chunk table: number chunks: %w", err)
	}
	if wp.numberChunks > 0 {
		if err := wp.enc.Init(wp.outstream); err != nil {
			return err
		}
		ic := NewIntegerCompressor(wp.enc, 32, 2, 8, 0)
		ic.InitCompressor()
		for i := uint32(0); i < wp.numberChunks; i++ {
			if wp.chunkSize == math.MaxUint32 {
				prev := int32(0)
				if i > 0 {
					prev = int32(wp.chunkSizes[i-1])
				}
				if err := ic.Compress(prev, int32(wp.chunkSizes[i]), 0); err != nil {
					return err
				}
			}
			prev := int32(0)
			if i > 0 {
				prev = int32(wp.chunkBytes[i-1])
			}
			if err := ic.Compress(prev, int32(wp.chunkBytes[i]), 1); err != nil {
				return err
			}
		}
		if _, err := wp.enc.Done(); err != nil {
			return err
		}
	}
	if wp.chunkTableStartPosition == -1 { // stream is not seekable
		var buf8 [8]byte
		binary.LittleEndian.PutUint64(buf8[:], uint64(position))
		if err := wp.outstream.Put64bitsLE(buf8[:]); err != nil {
			return fmt.Errorf("write chunk table: append position: %w", err)
		}
	}
	return nil
}
