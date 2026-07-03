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

// writeitem_raw_test.go — tests for the raw (uncompressed) item writers.
package laz

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// TestRawWriterPassthrough verifies that the fixed-size raw writers emit the
// item bytes unchanged.
func TestRawWriterPassthrough(t *testing.T) {
	mk := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*7 + 3)
		}
		return b
	}

	cases := []struct {
		name   string
		writer LASwriteItem
		size   int
	}{
		{"POINT10", &LASwriteItemRawPoint10LE{}, 20},
		{"GPSTIME11", &LASwriteItemRawGpsTime11LE{}, 8},
		{"RGB12", &LASwriteItemRawRGB12LE{}, 6},
		{"WAVEPACKET13", &LASwriteItemRawWavepacket13LE{}, 29},
		{"BYTE", NewLASwriteItemRawByte(5), 5},
		{"RGBNIR14", &LASwriteItemRawRGBNIR14LE{}, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewByteStreamOutArray()
			if err := tc.writer.(rawWriter).Init(out); err != nil {
				t.Fatalf("Init: %v", err)
			}
			item := mk(tc.size)
			var ctx uint32
			if err := tc.writer.Write(item, &ctx); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if !bytes.Equal(out.GetData(), item) {
				t.Fatalf("output mismatch:\n got %x\nwant %x", out.GetData(), item)
			}
		})
	}
}

// buildPoint14Disk builds a synthetic 30-byte on-disk PDRF6 record.
func buildPoint14Disk(x, y, z int32, intensity uint16, rn, nr, classFlags, scannerCh, scanDir, eofl, classification, userData uint8, scanAngle int16, psID uint16, gps float64) []byte {
	buf := make([]byte, 30)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(x))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(y))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(z))
	binary.LittleEndian.PutUint16(buf[12:14], intensity)
	buf[14] = (rn & 0x0F) | (nr << 4)
	buf[15] = (classFlags & 0x0F) | ((scannerCh & 0x03) << 4) | ((scanDir & 1) << 6) | ((eofl & 1) << 7)
	buf[16] = classification
	buf[17] = userData
	binary.LittleEndian.PutUint16(buf[18:20], uint16(scanAngle))
	binary.LittleEndian.PutUint16(buf[20:22], psID)
	binary.LittleEndian.PutUint64(buf[22:30], math.Float64bits(gps))
	return buf
}

// TestRawWriterPoint14Extended round-trips 30-byte disk records through the
// raw reader (30 → 40-byte in-memory layout) and back through the raw writer
// with the extended_point_type bit set (as the C++ write path provides it).
// The output must equal the original record byte-for-byte.
func TestRawWriterPoint14Extended(t *testing.T) {
	records := [][]byte{
		buildPoint14Disk(100, -200, 300, 512, 1, 2, 0x0, 0, 0, 0, 2, 7, 1500, 42, 123456.789),
		// classification >= 32 (not representable in the legacy 5-bit field)
		buildPoint14Disk(-1, -2, -3, 0, 3, 3, 0x5, 2, 1, 1, 64, 255, -3000, 65535, -1.5),
		// number of returns > 7 (legacy clamping path in the reader)
		buildPoint14Disk(2147483647, -2147483648, 0, 65535, 9, 12, 0x8, 3, 0, 1, 31, 0, 30000, 0, 0),
		// return number >= number of returns > 7
		buildPoint14Disk(7, 8, 9, 1, 15, 10, 0xF, 1, 1, 0, 0, 3, -30000, 9999, 1e300),
		// zero everything
		buildPoint14Disk(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0),
	}

	for i, disk := range records {
		// disk → 40-byte in-memory layout
		r := &LASreadItemRawPoint14LE{}
		if err := r.Init(NewByteStreamInArray(disk)); err != nil {
			t.Fatalf("record %d: reader init: %v", i, err)
		}
		item := make([]byte, 40)
		var ctx uint32
		if err := r.Read(item, &ctx); err != nil {
			t.Fatalf("record %d: read: %v", i, err)
		}
		// Mark the extended point type (the raw reader leaves it 0; the C++
		// write path always provides it for pf6-10 points).
		item[22] |= 1

		// 40-byte layout → disk
		w := &LASwriteItemRawPoint14LE{}
		out := NewByteStreamOutArray()
		if err := w.Init(out); err != nil {
			t.Fatalf("record %d: writer init: %v", i, err)
		}
		if err := w.Write(item, &ctx); err != nil {
			t.Fatalf("record %d: write: %v", i, err)
		}
		if !bytes.Equal(out.GetData(), disk) {
			t.Errorf("record %d: disk round-trip mismatch:\n got %x\nwant %x", i, out.GetData(), disk)
		}
	}
}

// TestRawWriterPoint14Legacy exercises the extended_point_type == 0 branch:
// legacy return fields are used, scanner channel is zeroed, and the scan
// angle is quantized from scan_angle_rank via I16_QUANTIZE(rank / 0.006f).
func TestRawWriterPoint14Legacy(t *testing.T) {
	cases := []struct {
		rank          int8
		wantScanAngle int16
	}{
		{0, 0},
		{15, 2500},   // 15/0.006 = 2500.0
		{-15, -2500}, // negative branch of I16_QUANTIZE
		{1, 167},     // 1/0.006 = 166.66.. → 167
		{-1, -167},
	}
	for _, tc := range cases {
		item := make([]byte, 40)
		// return_number=2, number_of_returns=3, scan_dir=1, eofl=0
		item[14] = 2 | (3 << 3) | (1 << 6)
		// classification byte: flags(withheld)=100b<<5 | class 17
		item[15] = (4 << 5) | 17
		item[16] = byte(tc.rank)
		item[17] = 99 // user data
		binary.LittleEndian.PutUint16(item[18:20], 777)
		binary.LittleEndian.PutUint64(item[32:40], math.Float64bits(42.5))
		// item[22] == 0: extended_point_type not set

		w := &LASwriteItemRawPoint14LE{}
		out := NewByteStreamOutArray()
		if err := w.Init(out); err != nil {
			t.Fatalf("writer init: %v", err)
		}
		var ctx uint32
		if err := w.Write(item, &ctx); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := out.GetData()
		if len(got) != 30 {
			t.Fatalf("expected 30 bytes, got %d", len(got))
		}
		if got[14] != 2|(3<<4) {
			t.Errorf("rank %d: returns byte = %02x, want %02x", tc.rank, got[14], 2|(3<<4))
		}
		// classification_flags = class byte >> 5 = 4; scanner channel = 0;
		// scan_dir = 1; eofl = 0
		if got[15] != 4|(1<<6) {
			t.Errorf("rank %d: flags byte = %02x, want %02x", tc.rank, got[15], 4|(1<<6))
		}
		if got[16] != 17 {
			t.Errorf("rank %d: classification = %d, want 17", tc.rank, got[16])
		}
		if got[17] != 99 {
			t.Errorf("rank %d: user data = %d, want 99", tc.rank, got[17])
		}
		gotAngle := int16(binary.LittleEndian.Uint16(got[18:20]))
		if gotAngle != tc.wantScanAngle {
			t.Errorf("rank %d: scan angle = %d, want %d", tc.rank, gotAngle, tc.wantScanAngle)
		}
		if binary.LittleEndian.Uint16(got[20:22]) != 777 {
			t.Errorf("rank %d: point source ID mismatch", tc.rank)
		}
		if math.Float64frombits(binary.LittleEndian.Uint64(got[22:30])) != 42.5 {
			t.Errorf("rank %d: gps time mismatch", tc.rank)
		}
	}
}
