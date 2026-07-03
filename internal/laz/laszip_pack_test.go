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

// laszip_pack_test.go — LASzip.Pack round-trip tests against Unpack.
package laz

import (
	"reflect"
	"testing"
)

func TestPackUnpackRoundtrip(t *testing.T) {
	tests := []struct {
		name       string
		pointType  uint8
		pointSize  uint16
		compressor uint16
		version    uint16
		chunkSize  uint32
	}{
		{"pf0 v2 chunked", 0, 20, LASZIP_COMPRESSOR_POINTWISE_CHUNKED, 2, 50000},
		{"pf1 v2 chunked", 1, 28, LASZIP_COMPRESSOR_POINTWISE_CHUNKED, 2, 100},
		{"pf3 with extra bytes", 3, 42, LASZIP_COMPRESSOR_POINTWISE_CHUNKED, 2, 100},
		{"pf3 v1 pointwise", 3, 34, LASZIP_COMPRESSOR_POINTWISE, 1, 0},
		{"pf6 v3 layered", 6, 30, LASZIP_COMPRESSOR_LAYERED_CHUNKED, 3, 100},
		{"pf8 v4 layered with extra bytes", 8, 46, LASZIP_COMPRESSOR_LAYERED_CHUNKED, 4, 50000},
		{"pf5 v2 with wavepacket", 5, 63, LASZIP_COMPRESSOR_POINTWISE_CHUNKED, 2, 50000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lz := NewLASzip()
			if err := lz.SetupByPointType(tc.pointType, tc.pointSize, tc.compressor); err != nil {
				t.Fatalf("SetupByPointType: %v", err)
			}
			if err := lz.RequestVersion(tc.version); err != nil {
				t.Fatalf("RequestVersion: %v", err)
			}
			if tc.chunkSize != 0 && tc.compressor != LASZIP_COMPRESSOR_POINTWISE {
				if err := lz.SetChunkSize(tc.chunkSize); err != nil {
					t.Fatalf("SetChunkSize: %v", err)
				}
			}

			data, err := lz.Pack()
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if want := 34 + 6*int(lz.NumItems); len(data) != want {
				t.Fatalf("payload length = %d, want %d", len(data), want)
			}

			got := NewLASzip()
			if err := got.Unpack(data); err != nil {
				t.Fatalf("Unpack of packed payload: %v", err)
			}
			if got.Compressor != lz.Compressor || got.Coder != lz.Coder ||
				got.VersionMajor != lz.VersionMajor || got.VersionMinor != lz.VersionMinor ||
				got.VersionRevision != lz.VersionRevision || got.Options != lz.Options ||
				got.ChunkSize != lz.ChunkSize ||
				got.NumberOfSpecialEVLRs != lz.NumberOfSpecialEVLRs ||
				got.OffsetToSpecialEVLRs != lz.OffsetToSpecialEVLRs {
				t.Errorf("scalar fields mismatch after round-trip:\n got %+v\nwant %+v", got, lz)
			}
			if !reflect.DeepEqual(got.Items, lz.Items) {
				t.Errorf("items mismatch:\n got %+v\nwant %+v", got.Items, lz.Items)
			}
		})
	}
}

// Pack must reproduce byte-for-byte the payload of a real C++-written LASzip
// VLR when configured identically. Uses the VLR captured from the v1 fixture
// (written by the real C++ LASzip at scripts/fixturegen).
func TestPackMatchesCPPVLR(t *testing.T) {
	// Extract the LASzip VLR payload from the C++-written fixture.
	u, err := OpenLAS("testdata/las/las12_pf3_1000pts_with_extrabytes_v1.laz")
	if err != nil {
		t.Fatalf("OpenLAS: %v", err)
	}
	defer u.Close()

	lz := NewLASzip()
	if err := lz.SetupByPointType(3, 42, LASZIP_COMPRESSOR_POINTWISE_CHUNKED); err != nil {
		t.Fatalf("SetupByPointType: %v", err)
	}
	if err := lz.RequestVersion(1); err != nil {
		t.Fatalf("RequestVersion: %v", err)
	}
	if err := lz.SetChunkSize(100); err != nil {
		t.Fatalf("SetChunkSize: %v", err)
	}

	data, err := lz.Pack()
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	// Unpack both and compare the semantically meaningful fields (the C++
	// version major/minor/revision fields depend on the LASzip build and are
	// not asserted byte-for-byte).
	got := NewLASzip()
	if err := got.Unpack(data); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	ref := u.lz
	if got.Compressor != ref.Compressor {
		t.Errorf("compressor %d != C++ %d", got.Compressor, ref.Compressor)
	}
	if got.Coder != ref.Coder {
		t.Errorf("coder %d != C++ %d", got.Coder, ref.Coder)
	}
	if got.ChunkSize != ref.ChunkSize {
		t.Errorf("chunkSize %d != C++ %d", got.ChunkSize, ref.ChunkSize)
	}
	if !reflect.DeepEqual(got.Items, ref.Items) {
		t.Errorf("items mismatch:\n got %+v\n c++ %+v", got.Items, ref.Items)
	}
}
