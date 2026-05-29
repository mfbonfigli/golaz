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

// Package laz provides LAZ (LASzip) decompression for LAS point cloud data.
//
// lasreadpoint.go — LASreadPoint orchestrator, ported from
// src/lasreadpoint.hpp/cpp.  Manages raw and compressed readers for all
// point items, chunk table parsing, seeking, and selective decompression.
package laz

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// debugStreamPos controls whether stream position logging is active.
// Set this from main or via the environment.
var debugStreamPos = false

func init() {
	if os.Getenv("LASZIP_DEBUG_STREAM") != "" {
		debugStreamPos = true
	}
}

// LASreadPoint orchestrates reading LAS/LAZ points.
type LASreadPoint struct {
	instream ByteStreamIn

	numReaders        uint32
	readers           []LASreadItem           // active readers (raw or compressed)
	readersRaw        []rawReader             // always populated (for Init)
	readersCompressed []LASreadItemCompressed // populated when compressing

	dec                     *ArithmeticDecoder
	layeredLAS14Compression bool

	// Chunking
	chunkSize    uint32
	chunkCount   uint32
	currentChunk uint32
	numberChunks uint32
	tabledChunks uint32
	chunkStarts  []int64
	chunkTotals  []uint32

	decompressSelective uint32

	// Seeking
	pointStart int64
	pointSize  uint32
	seekPoint  [][]byte

	lastError   string
	lastWarning string
}

// NewLASreadPoint creates a new orchestrator with the given selective
// decompression mask.
func NewLASreadPoint(decompressSelective uint32) *LASreadPoint {
	return &LASreadPoint{
		chunkSize:           math.MaxUint32,
		numberChunks:        math.MaxUint32,
		decompressSelective: decompressSelective,
	}
}

// Setup initialises the raw and (optionally) compressed readers based on
// the items described in laszip.  Must be called once before any read.
func (rp *LASreadPoint) Setup(numItems uint32, items []LASitem, lz *LASzip) error {
	if lz != nil {
		if numItems == 0 || items == nil {
			return fmt.Errorf("invalid input: numItems=%d, items=nil", numItems)
		}
		if numItems != uint32(lz.NumItems) {
			return fmt.Errorf("num_item mismatch: %d vs laszip %d", numItems, lz.NumItems)
		}
	}

	// --- entropy decoder --------------------------------------------------
	if rp.dec != nil {
		rp.dec = nil
		rp.layeredLAS14Compression = false
	}
	if lz != nil && lz.Compressor != LASZIP_COMPRESSOR_NONE {
		switch lz.Coder {
		case LASZIP_CODER_ARITHMETIC:
			rp.dec = NewArithmeticDecoder()
		default:
			return fmt.Errorf("coder %d not supported", lz.Coder)
		}
		rp.layeredLAS14Compression = (lz.Compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED)
	}

	// --- raw readers (always created) -------------------------------------
	rp.numReaders = numItems
	rp.pointSize = 0
	rp.readersRaw = make([]rawReader, numItems)
	for i := range numItems {
		sz := uint32(items[i].Size)
		switch items[i].Type {
		case LASITEM_POINT10:
			rp.readersRaw[i] = &LASreadItemRawPoint10LE{}
		case LASITEM_GPSTIME11:
			rp.readersRaw[i] = &LASreadItemRawGpsTime11LE{}
		case LASITEM_RGB12, LASITEM_RGB14:
			rp.readersRaw[i] = &LASreadItemRawRGB12LE{}
		case LASITEM_BYTE, LASITEM_BYTE14:
			rp.readersRaw[i] = NewLASreadItemRawByte(sz)
		case LASITEM_POINT14:
			rp.readersRaw[i] = &LASreadItemRawPoint14LE{}
		case LASITEM_RGBNIR14:
			rp.readersRaw[i] = &LASreadItemRawRGBNIR14LE{}
		case LASITEM_WAVEPACKET13, LASITEM_WAVEPACKET14:
			rp.readersRaw[i] = &LASreadItemRawWavepacket13LE{}
		default:
			return fmt.Errorf("item %d: unsupported type %d", i, items[i].Type)
		}
		rp.pointSize += sz
	}

	// --- compressed readers -----------------------------------------------
	if rp.dec != nil {
		rp.readersCompressed = make([]LASreadItemCompressed, numItems)

		var seekBuf []byte
		if rp.layeredLAS14Compression {
			seekBuf = make([]byte, rp.pointSize*2)
			seekBuf[22] = 1
		} else {
			seekBuf = make([]byte, rp.pointSize)
		}
		rp.seekPoint = make([][]byte, numItems)

		off := uint32(0)
		for i := range numItems {
			ver := items[i].Version
			if ver == 0 {
				ver = 2
			}
			sz := uint32(items[i].Size)
			switch items[i].Type {
			case LASITEM_POINT10:
				switch ver {
				case 1:
					rp.readersCompressed[i] = NewLASreadItemCompressedPoint10v1(rp.dec)
				case 2:
					rp.readersCompressed[i] = NewLASreadItemCompressedPoint10v2(rp.dec)
				default:
					return fmt.Errorf("POINT10 version %d unknown", ver)
				}
			case LASITEM_GPSTIME11:
				switch ver {
				case 1:
					rp.readersCompressed[i] = NewLASreadItemCompressedGpsTime11v1(rp.dec)
				case 2:
					rp.readersCompressed[i] = NewLASreadItemCompressedGpsTime11v2(rp.dec)
				default:
					return fmt.Errorf("GPSTIME11 version %d unknown", ver)
				}
			case LASITEM_RGB12:
				switch ver {
				case 1:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGB12v1(rp.dec)
				case 2:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGB12v2(rp.dec)
				default:
					return fmt.Errorf("RGB12 version %d unknown", ver)
				}
			case LASITEM_BYTE:
				switch ver {
				case 1:
					rp.readersCompressed[i] = NewLASreadItemCompressedBytev1(rp.dec, sz)
				case 2:
					rp.readersCompressed[i] = NewLASreadItemCompressedBytev2(rp.dec, sz)
				default:
					return fmt.Errorf("BYTE version %d unknown", ver)
				}
			case LASITEM_POINT14:
				switch ver {
				case 2, 3:
					rp.readersCompressed[i] = NewLASreadItemCompressedPoint14v3(rp.dec, rp.decompressSelective)
				case 4:
					rp.readersCompressed[i] = NewLASreadItemCompressedPoint14v4(rp.dec, rp.decompressSelective)
				default:
					return fmt.Errorf("POINT14 version %d unknown", ver)
				}
			case LASITEM_RGB14:
				switch ver {
				case 2, 3:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGB14v3(rp.dec, rp.decompressSelective)
				case 4:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGB14v4(rp.dec, rp.decompressSelective)
				default:
					return fmt.Errorf("RGB14 version %d unknown", ver)
				}
			case LASITEM_RGBNIR14:
				switch ver {
				case 2, 3:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGBNIR14v3(rp.dec, rp.decompressSelective)
				case 4:
					rp.readersCompressed[i] = NewLASreadItemCompressedRGBNIR14v4(rp.dec, rp.decompressSelective)
				default:
					return fmt.Errorf("RGBNIR14 version %d unknown", ver)
				}
			case LASITEM_BYTE14:
				switch ver {
				case 2, 3:
					rp.readersCompressed[i] = NewLASreadItemCompressedByte14v3(rp.dec, sz, rp.decompressSelective)
				case 4:
					rp.readersCompressed[i] = NewLASreadItemCompressedByte14v4(rp.dec, sz, rp.decompressSelective)
				default:
					return fmt.Errorf("BYTE14 version %d unknown", ver)
				}
			case LASITEM_WAVEPACKET13:
				if ver != 1 {
					return fmt.Errorf("WAVEPACKET13 version %d unknown", ver)
				}
				rp.readersCompressed[i] = NewLASreadItemCompressedWavepacket13v1(rp.dec)
			case LASITEM_WAVEPACKET14:
				switch ver {
				case 3:
					rp.readersCompressed[i] = NewLASreadItemCompressedWavepacket14v3(rp.dec, rp.decompressSelective)
				case 4:
					rp.readersCompressed[i] = NewLASreadItemCompressedWavepacket14v4(rp.dec, rp.decompressSelective)
				default:
					return fmt.Errorf("WAVEPACKET14 version %d unknown", ver)
				}
			}

			if rp.layeredLAS14Compression {
				rp.seekPoint[i] = seekBuf[off : off+2*sz]
				off += 2 * sz
			} else {
				rp.seekPoint[i] = seekBuf[off : off+sz]
				off += sz
			}
		}

		if lz.Compressor != LASZIP_COMPRESSOR_POINTWISE {
			if lz.ChunkSize != 0 {
				rp.chunkSize = lz.ChunkSize
			}
			rp.numberChunks = math.MaxUint32
		}
	}
	return nil
}

// Init binds the byte stream and initialises raw readers.
func (rp *LASreadPoint) Init(instream ByteStreamIn) error {
	if instream == nil {
		return fmt.Errorf("nil instream")
	}
	rp.instream = instream

	for i := uint32(0); i < rp.numReaders; i++ {
		if err := rp.readersRaw[i].Init(instream); err != nil {
			return err
		}
	}

	if rp.dec != nil {
		rp.chunkCount = rp.chunkSize
		rp.pointStart = 0
		rp.readers = nil
	} else {
		pos, err := instream.Tell()
		if err != nil {
			return err
		}
		rp.pointStart = pos
		rp.readers = make([]LASreadItem, rp.numReaders)
		for i := uint32(0); i < rp.numReaders; i++ {
			rp.readers[i] = rp.readersRaw[i].(LASreadItem)
		}
	}
	return nil
}

// Error returns the last error string.
func (rp *LASreadPoint) Error() string { return rp.lastError }

// Warning returns the last warning string.
func (rp *LASreadPoint) Warning() string { return rp.lastWarning }

// Done disassociates the stream.
func (rp *LASreadPoint) Done() {
	rp.instream = nil
}

// ---------------------------------------------------------------------------
// initDec — read chunk table (if needed), set point_start, reset readers.
// ---------------------------------------------------------------------------

func (rp *LASreadPoint) initDec() error {
	if rp.numberChunks == math.MaxUint32 {
		if err := rp.readChunkTable(); err != nil {
			return err
		}
		rp.currentChunk = 0
		if rp.chunkTotals != nil {
			rp.chunkSize = rp.chunkTotals[1]
		}
	}
	var err error
	rp.pointStart, err = rp.instream.Tell()
	if err != nil {
		return err
	}
	if debugStreamPos {
		fmt.Fprintf(os.Stderr, "STREAM: initDec() chunk=%d/%d pointStart=%d\n", rp.currentChunk, rp.numberChunks, rp.pointStart)
	}
	rp.readers = nil
	return nil
}

// ---------------------------------------------------------------------------
// readChunkTable — parse the chunk table at the end of the LAZ stream.
// On soft errors (recoverable with fixed chunk size) sets lastWarning
// and returns nil.  On hard errors returns a non-nil error.
// ---------------------------------------------------------------------------

func (rp *LASreadPoint) readChunkTable() error {
	buf := make([]byte, 8)

	// Read the 8 bytes that store the location of the chunk table
	if err := rp.instream.Get64bitsLE(buf); err != nil {
		return err
	}
	chunkTableStart := int64(binary.LittleEndian.Uint64(buf))

	// This is where the chunks start
	chunksStart, err := rp.instream.Tell()
	if err != nil {
		return err
	}

	// Was compressor interrupted before writing chunk table?
	if (chunkTableStart + 8) == chunksStart {
		if rp.chunkSize == math.MaxUint32 {
			rp.lastError = "compressor was interrupted before writing adaptive chunk table of LAZ file"
			return fmt.Errorf("%s", rp.lastError)
		}
		rp.numberChunks = 256
		rp.chunkStarts = make([]int64, rp.numberChunks+1)
		rp.chunkStarts[0] = chunksStart
		rp.tabledChunks = 1
		rp.lastWarning = "compressor was interrupted before writing chunk table of LAZ file"
		return nil
	}

	// Stream not seekable? With fixed chunk size we don't need it.
	if !rp.instream.IsSeekable() {
		if rp.chunkSize == math.MaxUint32 {
			return fmt.Errorf("non-seekable stream with adaptive chunking")
		}
		rp.numberChunks = 0
		rp.tabledChunks = 0
		return nil
	}

	// chunk_table_start_position == -1 means compressor wrote to non-seekable
	// stream and put the pointer at the end.
	if chunkTableStart == -1 {
		if err := rp.instream.SeekEnd(8); err != nil {
			return err
		}
		buf2 := make([]byte, 8)
		if err := rp.instream.Get64bitsLE(buf2); err != nil {
			return err
		}
		chunkTableStart = int64(binary.LittleEndian.Uint64(buf2))
	}

	// Seek to chunk table position
	if err := rp.instream.Seek(chunkTableStart); err != nil {
		return rp.chunkTableFallback(chunksStart, chunkTableStart)
	}
	pos, err := rp.instream.Tell()
	if err != nil || pos != chunkTableStart {
		return rp.chunkTableFallback(chunksStart, chunkTableStart)
	}

	// Read version (must be 0)
	verBuf := make([]byte, 4)
	if err := rp.instream.Get32bitsLE(verBuf); err != nil {
		return rp.chunkTableFallback(chunksStart, chunkTableStart)
	}
	if binary.LittleEndian.Uint32(verBuf) != 0 {
		return rp.chunkTableFallback(chunksStart, chunkTableStart)
	}

	// Read number of chunks
	if err := rp.instream.Get32bitsLE(verBuf); err != nil {
		return rp.chunkTableFallback(chunksStart, chunkTableStart)
	}
	numberChunks := binary.LittleEndian.Uint32(verBuf)

	rp.chunkTotals = nil
	if rp.chunkSize == math.MaxUint32 {
		rp.chunkTotals = make([]uint32, numberChunks+1)
		rp.chunkTotals[0] = 0
	}
	rp.chunkStarts = make([]int64, numberChunks+1)
	rp.chunkStarts[0] = chunksStart
	rp.tabledChunks = 1

	if numberChunks > 0 {
		rp.dec.Init(rp.instream, true)
		ic := NewIntegerDecompressor(rp.dec, 32, 2, 8, 0)
		ic.InitDecompressor()
		for i := uint32(1); i <= numberChunks; i++ {
			if rp.chunkSize == math.MaxUint32 {
				prev := uint32(0)
				if i > 1 {
					prev = rp.chunkTotals[i-1]
				}
				val, err := ic.Decompress(int32(prev), 0)
				if err != nil {
					rp.dec.Done()
					return rp.chunkTableFallback(chunksStart, chunkTableStart)
				}
				rp.chunkTotals[i] = uint32(val)
			}
			prevStart := int64(0)
			if i > 1 {
				prevStart = rp.chunkStarts[i-1]
			}
			val, err := ic.Decompress(int32(prevStart), 1)
			if err != nil {
				rp.dec.Done()
				return rp.chunkTableFallback(chunksStart, chunkTableStart)
			}
			rp.chunkStarts[i] = int64(val)
			rp.tabledChunks++
		}
		rp.dec.Done()

		// Accumulate
		for i := uint32(1); i <= numberChunks; i++ {
			if rp.chunkSize == math.MaxUint32 {
				rp.chunkTotals[i] += rp.chunkTotals[i-1]
			}
			rp.chunkStarts[i] += rp.chunkStarts[i-1]
			if rp.chunkStarts[i] <= rp.chunkStarts[i-1] {
				return rp.chunkTableFallback(chunksStart, chunkTableStart)
			}
		}
	}

	rp.numberChunks = numberChunks
	_ = rp.instream.Seek(chunksStart)
	return nil
}

// chunkTableFallback handles corrupt/missing chunk table recovery.
// Returns nil on soft error (fixed chunk size), non-nil on hard error.
func (rp *LASreadPoint) chunkTableFallback(chunksStart, chunkTableStart int64) error {
	rp.chunkTotals = nil
	if rp.chunkSize == math.MaxUint32 {
		return fmt.Errorf("corrupt chunk table with adaptive chunking")
	}
	if rp.numberChunks == math.MaxUint32 {
		rp.numberChunks = 256
		rp.chunkStarts = make([]int64, rp.numberChunks+1)
		rp.chunkStarts[0] = chunksStart
		rp.tabledChunks = 1
	} else {
		for i := uint32(1); i < rp.tabledChunks; i++ {
			rp.chunkStarts[i] += rp.chunkStarts[i-1]
		}
	}
	// Determine warning
	_ = rp.instream.SeekEnd(0)
	lastPos, _ := rp.instream.Tell()
	if lastPos <= chunkTableStart {
		if lastPos == chunkTableStart {
			rp.lastWarning = "chunk table is missing. improper use of LAZ compressor?"
		} else {
			rp.lastWarning = fmt.Sprintf("chunk table and %d bytes are missing. LAZ file truncated during copy or transfer?", chunkTableStart-lastPos)
		}
	} else {
		rp.lastWarning = "corrupt chunk table"
	}
	return nil
}

// ---------------------------------------------------------------------------
// searchChunkTable — binary search over chunk_totals.
// ---------------------------------------------------------------------------

func (rp *LASreadPoint) searchChunkTable(index, lower, upper uint32) uint32 {
	if lower+1 == upper {
		return lower
	}
	mid := (lower + upper) / 2
	if index >= rp.chunkTotals[mid] {
		return rp.searchChunkTable(index, mid, upper)
	}
	return rp.searchChunkTable(index, lower, mid)
}

// ---------------------------------------------------------------------------
// Read — decompress one point.  point[i] holds the buffer for item i.
// ---------------------------------------------------------------------------

func (rp *LASreadPoint) Read(point [][]byte) error {
	context := uint32(0)

	if rp.dec != nil {
		// ---------- compressed path ----------
		if rp.chunkCount == rp.chunkSize {
			if rp.pointStart != 0 {
				rp.dec.Done()
				rp.currentChunk++
				if rp.currentChunk < rp.tabledChunks {
					here, err := rp.instream.Tell()
					if err != nil {
						return err
					}
					if debugStreamPos {
						fmt.Fprintf(os.Stderr, "STREAM: chunk boundary chunk=%d expected=%d actual=%d diff=%d\n",
							rp.currentChunk, rp.chunkStarts[rp.currentChunk], here, here-rp.chunkStarts[rp.currentChunk])
					}
					if rp.chunkStarts[rp.currentChunk] != here {
						rp.currentChunk--
						rp.lastError = fmt.Sprintf("chunk with index %d of %d is corrupt", rp.currentChunk, rp.tabledChunks)
						if (rp.currentChunk + 1) < rp.tabledChunks {
							_ = rp.instream.Seek(rp.chunkStarts[rp.currentChunk+1])
							rp.chunkCount = rp.chunkSize
						}
						return fmt.Errorf("%s", rp.lastError)
					}
				}
			}
			if err := rp.initDec(); err != nil {
				return err
			}
			if rp.currentChunk == rp.tabledChunks {
				if rp.currentChunk >= rp.numberChunks {
					rp.numberChunks += 256
					newStarts := make([]int64, rp.numberChunks+1)
					copy(newStarts, rp.chunkStarts)
					rp.chunkStarts = newStarts
				}
				rp.chunkStarts[rp.tabledChunks] = rp.pointStart
				rp.tabledChunks++
			} else if rp.chunkTotals != nil {
				rp.chunkSize = rp.chunkTotals[rp.currentChunk+1] - rp.chunkTotals[rp.currentChunk]
			}
			rp.chunkCount = 0
		}
		rp.chunkCount++

		if rp.readers != nil {
			for i := uint32(0); i < rp.numReaders; i++ {
				if err := rp.readers[i].Read(point[i], &context); err != nil {
					return err
				}
			}
		} else {
			// First point in chunk: read raw then init compressed readers
			for i := uint32(0); i < rp.numReaders; i++ {
				r := rp.readersRaw[i].(LASreadItem)
				if err := r.Read(point[i], &context); err != nil {
					return err
				}
			}
			if rp.layeredLAS14Compression {
				// 'dec' only hands over the stream (doesn't decode)
				rp.dec.Init(rp.instream, false)
				countBuf := make([]byte, 4)
				if err := rp.instream.Get32bitsLE(countBuf); err != nil {
					return err
				}
				// num_points_in_chunk: read to advance stream position; value not used by layered decompressor
				_ = binary.LittleEndian.Uint32(countBuf)
				for i := uint32(0); i < rp.numReaders; i++ {
					if err := rp.readersCompressed[i].ChunkSizes(); err != nil {
						return err
					}
				}
				for i := uint32(0); i < rp.numReaders; i++ {
					if err := rp.readersCompressed[i].Init(point[i], &context); err != nil {
						return err
					}
				}
			} else {
				for i := uint32(0); i < rp.numReaders; i++ {
					if err := rp.readersCompressed[i].Init(point[i], &context); err != nil {
						return err
					}
				}
				rp.dec.Init(rp.instream, true)
			}
			rp.readers = make([]LASreadItem, rp.numReaders)
			for i := uint32(0); i < rp.numReaders; i++ {
				rp.readers[i] = rp.readersCompressed[i]
			}
		}
	} else {
		// ---------- uncompressed path ----------
		for i := uint32(0); i < rp.numReaders; i++ {
			if err := rp.readers[i].Read(point[i], &context); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Seek — jump to the target point by index.
// ---------------------------------------------------------------------------

func (rp *LASreadPoint) Seek(current, target uint32) error {
	if !rp.instream.IsSeekable() {
		return fmt.Errorf("stream not seekable")
	}
	delta := uint32(0)
	if rp.dec != nil {
		if rp.pointStart == 0 {
			if err := rp.initDec(); err != nil {
				return err
			}
			rp.chunkCount = 0
		}
		if rp.chunkStarts != nil {
			var targetChunk uint32
			if rp.chunkTotals != nil {
				targetChunk = rp.searchChunkTable(target, 0, rp.numberChunks)
				rp.chunkSize = rp.chunkTotals[targetChunk+1] - rp.chunkTotals[targetChunk]
				delta = target - rp.chunkTotals[targetChunk]
			} else {
				targetChunk = target / rp.chunkSize
				delta = target % rp.chunkSize
			}
			if targetChunk >= rp.tabledChunks {
				if rp.currentChunk < (rp.tabledChunks - 1) {
					rp.dec.Done()
					rp.currentChunk = rp.tabledChunks - 1
					if err := rp.instream.Seek(rp.chunkStarts[rp.currentChunk]); err != nil {
						return err
					}
					if err := rp.initDec(); err != nil {
						return err
					}
					rp.chunkCount = 0
				}
				delta += (rp.chunkSize*(targetChunk-rp.currentChunk) - rp.chunkCount)
			} else if rp.currentChunk != targetChunk || current > target {
				rp.dec.Done()
				rp.currentChunk = targetChunk
				if err := rp.instream.Seek(rp.chunkStarts[rp.currentChunk]); err != nil {
					return err
				}
				if err := rp.initDec(); err != nil {
					return err
				}
				rp.chunkCount = 0
			} else {
				delta = target - current
			}
		} else if current > target {
			rp.dec.Done()
			if err := rp.instream.Seek(rp.pointStart); err != nil {
				return err
			}
			if err := rp.initDec(); err != nil {
				return err
			}
			delta = target
		} else if current < target {
			delta = target - current
		}
		for delta > 0 {
			if err := rp.Read(rp.seekPoint); err != nil {
				return err
			}
			delta--
		}
	} else {
		if current != target {
			targetPos := rp.pointStart + int64(rp.pointSize)*int64(target)
			if err := rp.instream.Seek(targetPos); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckEnd — verify integrity after reading the last point.
func (rp *LASreadPoint) CheckEnd() error {
	if rp.dec != nil {
		rp.dec.Done()
		rp.currentChunk++
		if rp.currentChunk < rp.tabledChunks {
			here, err := rp.instream.Tell()
			if err != nil {
				return err
			}
			if rp.chunkStarts[rp.currentChunk] != here {
				rp.lastError = fmt.Sprintf("chunk with index %d of %d is corrupt", rp.currentChunk, rp.tabledChunks)
				return fmt.Errorf("%s", rp.lastError)
			}
		}
	}
	return nil
}
