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

// robustness_test.go — table-driven tests for behavioral parity with the
// LASzip C++ reference (lasreadpoint.cpp, arithmeticdecoder.cpp) on
// malformed or unusual inputs. Expected behavior mirrors the C++ code paths;
// where C++ has undefined behavior (overflow, OOB reads) the expectation is
// a clean Go error instead of a panic.
package laz

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// LASreadPoint.Setup: item version 0 must be rejected for compressed items
// (C++ lasreadpoint.cpp returns FALSE for version 0 of every item type).
// ---------------------------------------------------------------------------

func TestSetupRejectsVersion0Items(t *testing.T) {
	tests := []struct {
		name string
		item LASitem
	}{
		{"POINT10 v0", LASitem{Type: LASITEM_POINT10, Size: 20, Version: 0}},
		{"GPSTIME11 v0", LASitem{Type: LASITEM_GPSTIME11, Size: 8, Version: 0}},
		{"RGB12 v0", LASitem{Type: LASITEM_RGB12, Size: 6, Version: 0}},
		{"BYTE v0", LASitem{Type: LASITEM_BYTE, Size: 4, Version: 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lz := NewLASzip()
			lz.Compressor = LASZIP_COMPRESSOR_POINTWISE_CHUNKED
			lz.NumItems = 1
			lz.Items = []LASitem{tc.item}

			rp := NewLASreadPoint(math.MaxUint32)
			if err := rp.Setup(1, lz.Items, lz); err == nil {
				t.Errorf("Setup accepted %s; C++ lasreadpoint::setup rejects version 0", tc.name)
			}
		})
	}
}

// POINT14 v0 must also be rejected (C++ accepts only 2/3/4 in the switch).
func TestSetupRejectsPoint14Version0(t *testing.T) {
	lz := NewLASzip()
	lz.Compressor = LASZIP_COMPRESSOR_LAYERED_CHUNKED
	lz.NumItems = 1
	lz.Items = []LASitem{{Type: LASITEM_POINT14, Size: 30, Version: 0}}

	rp := NewLASreadPoint(math.MaxUint32)
	if err := rp.Setup(1, lz.Items, lz); err == nil {
		t.Error("Setup accepted POINT14 v0; C++ rejects version 0")
	}
}

// ---------------------------------------------------------------------------
// LASreadPoint.Setup: layered compressor requires a POINT14 first item
// (real layered files always have one; C++ silently overflows seek_point[22]
// otherwise — Go must return an error, not panic).
// ---------------------------------------------------------------------------

func TestSetupLayeredRequiresPoint14First(t *testing.T) {
	tests := []struct {
		name    string
		items   []LASitem
		wantErr bool
	}{
		{
			"layered with lone RGB14 (would panic on seekBuf)",
			[]LASitem{{Type: LASITEM_RGB14, Size: 6, Version: 3}},
			true,
		},
		{
			"layered with POINT14 first ok",
			[]LASitem{{Type: LASITEM_POINT14, Size: 30, Version: 3}},
			false,
		},
		{
			"layered with POINT14+RGB14 ok",
			[]LASitem{
				{Type: LASITEM_POINT14, Size: 30, Version: 3},
				{Type: LASITEM_RGB14, Size: 6, Version: 3},
			},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lz := NewLASzip()
			lz.Compressor = LASZIP_COMPRESSOR_LAYERED_CHUNKED
			lz.NumItems = uint16(len(tc.items))
			lz.Items = tc.items

			rp := NewLASreadPoint(math.MaxUint32)
			err := rp.Setup(uint32(len(tc.items)), tc.items, lz)
			if tc.wantErr && err == nil {
				t.Error("Setup succeeded, want error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Setup: %v", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Chunk table fallback must reposition the stream at chunksStart so that
// sequential reading can proceed (C++ lasreadpoint.cpp:781 seeks in all paths).
// ---------------------------------------------------------------------------

// newChunkedReadPoint builds a LASreadPoint wired for a fixed-chunk-size
// compressed stream, bypassing Setup (white-box).
func newChunkedReadPoint(data []byte, chunkSize uint32) *LASreadPoint {
	rp := NewLASreadPoint(math.MaxUint32)
	rp.dec = NewArithmeticDecoder()
	rp.chunkSize = chunkSize
	rp.numberChunks = math.MaxUint32
	rp.instream = NewByteStreamInArray(data)
	return rp
}

func TestChunkTableFallbackSeeksBackToChunksStart(t *testing.T) {
	// 8-byte table offset pointing far beyond the stream, then filler bytes
	// standing in for compressed chunk data.
	data := make([]byte, 8+64)
	binary.LittleEndian.PutUint64(data[0:8], 0x7FFFFFFF)

	rp := newChunkedReadPoint(data, 50000)
	if err := rp.readChunkTable(); err != nil {
		t.Fatalf("readChunkTable returned hard error %v; C++ recovers with a warning", err)
	}
	if rp.lastWarning == "" {
		t.Error("expected a warning after chunk table fallback")
	}
	pos, err := rp.instream.Tell()
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}
	if pos != 8 {
		t.Errorf("stream position after fallback = %d, want 8 (chunksStart); sequential chunk reading is impossible otherwise", pos)
	}
	if rp.tabledChunks != 1 {
		t.Errorf("tabledChunks = %d, want 1", rp.tabledChunks)
	}
}

// ---------------------------------------------------------------------------
// A corrupt number_chunks must not trigger a multi-GB allocation: it cannot
// plausibly exceed the byte span between chunksStart and the chunk table.
// ---------------------------------------------------------------------------

func TestReadChunkTableCorruptNumberChunksCap(t *testing.T) {
	// Table immediately follows the 8-byte offset slot: version 0, then an
	// absurd number_chunks. chunkTableStart(8) - chunksStart(8) = 0, so any
	// nonzero chunk count is impossible.
	data := make([]byte, 8+8+16)
	binary.LittleEndian.PutUint64(data[0:8], 8)         // table position
	binary.LittleEndian.PutUint32(data[8:12], 0)        // version
	binary.LittleEndian.PutUint32(data[12:16], 1<<31-1) // number_chunks: absurd

	rp := newChunkedReadPoint(data, 50000)
	if err := rp.readChunkTable(); err != nil {
		t.Fatalf("readChunkTable returned hard error %v; want soft fallback", err)
	}
	if rp.lastWarning == "" {
		t.Error("expected a warning after rejecting corrupt number_chunks")
	}
	pos, _ := rp.instream.Tell()
	if pos != 8 {
		t.Errorf("stream position after fallback = %d, want 8 (chunksStart)", pos)
	}
}

// ---------------------------------------------------------------------------
// Adaptive chunking with an empty chunk table must error, not panic on
// chunkTotals[1] (C++ reads past its allocation here — UB).
// ---------------------------------------------------------------------------

func TestInitDecZeroChunksAdaptiveNoPanic(t *testing.T) {
	data := make([]byte, 8+8+16)
	binary.LittleEndian.PutUint64(data[0:8], 8)   // table position
	binary.LittleEndian.PutUint32(data[8:12], 0)  // version
	binary.LittleEndian.PutUint32(data[12:16], 0) // number_chunks = 0

	rp := newChunkedReadPoint(data, math.MaxUint32) // adaptive
	if err := rp.initDec(); err == nil {
		t.Error("initDec succeeded on empty adaptive chunk table; want error")
	}
}

// ---------------------------------------------------------------------------
// CheckEnd after a Seek that landed on a chunk boundary (no point read since)
// must be a no-op: C++ gates check_end() on readers == readers_compressed.
// ---------------------------------------------------------------------------

func TestCheckEndAfterSeek(t *testing.T) {
	path := filepath.Join("testdata", "las", "las12_pf0_1000pts_with_extrabytes.laz")

	t.Run("seek to chunk boundary then CheckEnd", func(t *testing.T) {
		u, err := OpenLAS(path)
		if err != nil {
			t.Fatalf("OpenLAS: %v", err)
		}
		defer u.Close()
		if err := u.Seek(200); err != nil { // chunk size 100 → exact boundary
			t.Fatalf("Seek: %v", err)
		}
		if err := u.reader.CheckEnd(); err != nil {
			t.Errorf("CheckEnd after seek-without-read: %v; C++ skips the check when no compressed reader is engaged", err)
		}
	})

	t.Run("full sequential read then CheckEnd", func(t *testing.T) {
		u, err := OpenLAS(path)
		if err != nil {
			t.Fatalf("OpenLAS: %v", err)
		}
		defer u.Close()
		items := u.Items()
		offsets := u.Offsets()
		buf := make([]byte, offsets[len(offsets)-1])
		pt := make([][]byte, len(items))
		for j := range items {
			pt[j] = buf[offsets[j]:offsets[j+1]]
		}
		for i := uint32(0); i < u.NumPoints(); i++ {
			if err := u.Read(pt); err != nil {
				t.Fatalf("Read %d: %v", i, err)
			}
		}
		if err := u.reader.CheckEnd(); err != nil {
			t.Errorf("CheckEnd after full read: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// IsStandard must recognize genuine point-format-10 item sets. Upstream C++
// laszip.cpp has an indexing typo in this branch (items[1] tested for
// WAVEPACKET14 where items[2] is meant) so it reports real pf10 files as
// non-standard; golaz deliberately applies the intended check. The golden
// laszip_config.bin comparison carves out exactly this divergence.
// ---------------------------------------------------------------------------

func TestIsStandardPointType10(t *testing.T) {
	tests := []struct {
		name     string
		items    []LASitem
		wantStd  bool
		wantType uint8
		wantLen  uint16
	}{
		{
			"pf10: POINT14+RGBNIR14+WAVEPACKET14",
			[]LASitem{{Type: LASITEM_POINT14, Size: 30}, {Type: LASITEM_RGBNIR14, Size: 8}, {Type: LASITEM_WAVEPACKET14, Size: 29}},
			true, 10, 67,
		},
		{
			"pf10 with extra bytes: POINT14+RGBNIR14+WAVEPACKET14+BYTE14",
			[]LASitem{{Type: LASITEM_POINT14, Size: 30}, {Type: LASITEM_RGBNIR14, Size: 8}, {Type: LASITEM_WAVEPACKET14, Size: 29}, {Type: LASITEM_BYTE14, Size: 4}},
			true, 10, 71,
		},
		{
			"pf10 with legacy wavepacket item: POINT14+RGBNIR14+WAVEPACKET13",
			[]LASitem{{Type: LASITEM_POINT14, Size: 30}, {Type: LASITEM_RGBNIR14, Size: 8}, {Type: LASITEM_WAVEPACKET13, Size: 29}},
			true, 10, 67,
		},
		{
			"pf8 regression: POINT14+RGBNIR14+BYTE14",
			[]LASitem{{Type: LASITEM_POINT14, Size: 30}, {Type: LASITEM_RGBNIR14, Size: 8}, {Type: LASITEM_BYTE14, Size: 4}},
			true, 8, 42,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lz := LASzip{NumItems: uint16(len(tc.items)), Items: tc.items}
			var pt uint8
			var rl uint16
			got := lz.IsStandard(&pt, &rl)
			if got != tc.wantStd {
				t.Fatalf("IsStandard = %v, want %v", got, tc.wantStd)
			}
			if !got {
				return
			}
			if pt != tc.wantType {
				t.Errorf("pointType = %d, want %d", pt, tc.wantType)
			}
			if rl != tc.wantLen {
				t.Errorf("recordLength = %d, want %d", rl, tc.wantLen)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Raw arithmetic reads must detect out-of-range symbols on corrupt streams
// (C++ arithmeticdecoder.cpp throws 4711 in readBit/readBits/readByte/readShort;
// integrity checks added upstream on 13 Nov 2014).
// ---------------------------------------------------------------------------

func TestArithmeticDecoderRawReadGuards(t *testing.T) {
	// A stream of 0xFF seeds value = 0xFFFFFFFF with length = 0xFFFFFFFF,
	// which produces sym == 1<<bits (out of range) in every raw read.
	corrupt := func(t *testing.T) *ArithmeticDecoder {
		t.Helper()
		d := NewArithmeticDecoder()
		data := make([]byte, 16)
		for i := range data {
			data[i] = 0xFF
		}
		if err := d.Init(NewByteStreamInArray(data), true); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return d
	}

	t.Run("ReadBit", func(t *testing.T) {
		d := corrupt(t)
		if v, err := d.ReadBit(); err == nil {
			t.Errorf("ReadBit = %d, <nil>; want out-of-range error (sym 2 > 1)", v)
		}
	})
	t.Run("ReadBits", func(t *testing.T) {
		d := corrupt(t)
		if v, err := d.ReadBits(8); err == nil {
			t.Errorf("ReadBits(8) = %d, <nil>; want out-of-range error (sym 256)", v)
		}
	})
	t.Run("ReadByte", func(t *testing.T) {
		d := corrupt(t)
		if v, err := d.ReadByte(); err == nil {
			t.Errorf("ReadByte = %d, <nil>; want out-of-range error (sym 256 truncated)", v)
		}
	})
	t.Run("ReadShort", func(t *testing.T) {
		d := corrupt(t)
		if v, err := d.ReadShort(); err == nil {
			t.Errorf("ReadShort = %d, <nil>; want out-of-range error (sym 65537 truncated)", v)
		}
	})
}
