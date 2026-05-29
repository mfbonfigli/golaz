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
	"encoding/binary"
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// StreamingMedian5 — cross-validate against C++ reference output
// ---------------------------------------------------------------------------

func TestStreamingMedian5(t *testing.T) {
	raw, err := os.ReadFile("testdata/median5.bin")
	if err != nil {
		t.Fatalf("cannot read median5 vectors: %v", err)
	}

	// 5 sequences × 10 values × (int32 input + int32 median) = 400 bytes
	if len(raw) != 400 {
		t.Fatalf("unexpected median5.bin size: %d, want 400", len(raw))
	}

	off := 0
	for seqIdx := range 5 {
		m := NewStreamingMedian5()
		for j := range 10 {
			input := int32(binary.LittleEndian.Uint32(raw[off : off+4]))
			off += 4
			expectedMedian := int32(binary.LittleEndian.Uint32(raw[off : off+4]))
			off += 4
			m.Add(input)
			got := m.Get()
			if got != expectedMedian {
				t.Errorf("seq=%d step=%d: add(%d) -> median got=%d, want=%d (values=%v)",
					seqIdx, j, input, got, expectedMedian, m.Values)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Lookup tables — cross-validate against C++ reference dump
// ---------------------------------------------------------------------------

func TestLookupTables(t *testing.T) {
	raw, err := os.ReadFile("testdata/lkup.bin")
	if err != nil {
		t.Fatalf("cannot read lkup vectors: %v", err)
	}
	if len(raw) != 128 {
		t.Fatalf("unexpected lkup.bin size: %d, want 128", len(raw))
	}

	off := 0

	// number_return_map[8][8] = 64 bytes
	for n := range 8 {
		for r := range 8 {
			want := raw[off]
			off++
			got := NumberReturnMap[n][r]
			if got != want {
				t.Errorf("NumberReturnMap[%d][%d]: got=%d, want=%d", n, r, got, want)
			}
		}
	}

	// number_return_level[8][8] = 64 bytes
	for n := range 8 {
		for r := range 8 {
			want := raw[off]
			off++
			got := NumberReturnLevel[n][r]
			if got != want {
				t.Errorf("NumberReturnLevel[%d][%d]: got=%d, want=%d", n, r, got, want)
			}
		}
	}

}

// ---------------------------------------------------------------------------
// Selective decompression constants — verify bitmask values
// ---------------------------------------------------------------------------

func TestSelectiveDecompressionMasks(t *testing.T) {
	tests := []struct {
		name  string
		value uint32
		want  uint32
	}{
		{"ALL", LASZIP_DECOMPRESS_SELECTIVE_ALL, 0xFFFFFFFF},
		{"CHANNEL_RETURNS_XY", LASZIP_DECOMPRESS_SELECTIVE_CHANNEL_RETURNS_XY, 0x00000000},
		{"Z", LASZIP_DECOMPRESS_SELECTIVE_Z, 0x00000001},
		{"CLASSIFICATION", LASZIP_DECOMPRESS_SELECTIVE_CLASSIFICATION, 0x00000002},
		{"FLAGS", LASZIP_DECOMPRESS_SELECTIVE_FLAGS, 0x00000004},
		{"INTENSITY", LASZIP_DECOMPRESS_SELECTIVE_INTENSITY, 0x00000008},
		{"SCAN_ANGLE", LASZIP_DECOMPRESS_SELECTIVE_SCAN_ANGLE, 0x00000010},
		{"USER_DATA", LASZIP_DECOMPRESS_SELECTIVE_USER_DATA, 0x00000020},
		{"POINT_SOURCE", LASZIP_DECOMPRESS_SELECTIVE_POINT_SOURCE, 0x00000040},
		{"GPS_TIME", LASZIP_DECOMPRESS_SELECTIVE_GPS_TIME, 0x00000080},
		{"RGB", LASZIP_DECOMPRESS_SELECTIVE_RGB, 0x00000100},
		{"NIR", LASZIP_DECOMPRESS_SELECTIVE_NIR, 0x00000200},
		{"WAVEPACKET", LASZIP_DECOMPRESS_SELECTIVE_WAVEPACKET, 0x00000400},
		{"BYTE0", LASZIP_DECOMPRESS_SELECTIVE_BYTE0, 0x00010000},
		{"BYTE1", LASZIP_DECOMPRESS_SELECTIVE_BYTE1, 0x00020000},
		{"BYTE2", LASZIP_DECOMPRESS_SELECTIVE_BYTE2, 0x00040000},
		{"BYTE3", LASZIP_DECOMPRESS_SELECTIVE_BYTE3, 0x00080000},
		{"BYTE4", LASZIP_DECOMPRESS_SELECTIVE_BYTE4, 0x00100000},
		{"BYTE5", LASZIP_DECOMPRESS_SELECTIVE_BYTE5, 0x00200000},
		{"BYTE6", LASZIP_DECOMPRESS_SELECTIVE_BYTE6, 0x00400000},
		{"BYTE7", LASZIP_DECOMPRESS_SELECTIVE_BYTE7, 0x00800000},
		{"EXTRA_BYTES", LASZIP_DECOMPRESS_SELECTIVE_EXTRA_BYTES, 0xFFFF0000},
	}
	for _, tc := range tests {
		if tc.value != tc.want {
			t.Errorf("%s: got=%#08x, want=%#08x", tc.name, tc.value, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// LASwavepacket13 roundtrip
// ---------------------------------------------------------------------------

func TestLASwavepacket13Roundtrip(t *testing.T) {
	wp := LASwavepacket13{
		Offset:      0x1234567890ABCDEF,
		PacketSize:  42,
		ReturnPoint: 3.14,
		X:           -100.5,
		Y:           2000.25,
		Z:           0.0,
	}
	packed := PackLASwavepacket13(&wp)
	unpacked := UnpackLASwavepacket13(packed)

	if unpacked != wp {
		t.Errorf("roundtrip mismatch:\n  original: %+v\n  unpacked: %+v", wp, unpacked)
	}
	if len(packed) != 29 {
		t.Errorf("unexpected packed size %d, want 29", len(packed))
	}
}

// ---------------------------------------------------------------------------
// StreamingMedian5 standalone: after init all values are zero, get returns 0
// ---------------------------------------------------------------------------

func TestStreamingMedian5Init(t *testing.T) {
	m := NewStreamingMedian5()
	if m.Get() != 0 {
		t.Errorf("after init, get()=%d want 0", m.Get())
	}
	for i := range m.Values {
		if m.Values[i] != 0 {
			t.Errorf("after init, values[%d]=%d want 0", i, m.Values[i])
		}
	}
	if !m.High {
		t.Error("after init, high=false, want true")
	}
}
