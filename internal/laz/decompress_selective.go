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
// decompress_selective.go — selective decompression bitmask constants ported
// from src/laszip_decompress_selective_v3.hpp.
package laz

// Selective decompression flags for v3/v4 layered compression.
// These masks control which point attributes are decompressed.
const (
	LASZIP_DECOMPRESS_SELECTIVE_ALL                uint32 = 0xFFFFFFFF
	LASZIP_DECOMPRESS_SELECTIVE_CHANNEL_RETURNS_XY uint32 = 0x00000000
	LASZIP_DECOMPRESS_SELECTIVE_Z                  uint32 = 0x00000001
	LASZIP_DECOMPRESS_SELECTIVE_CLASSIFICATION     uint32 = 0x00000002
	LASZIP_DECOMPRESS_SELECTIVE_FLAGS              uint32 = 0x00000004
	LASZIP_DECOMPRESS_SELECTIVE_INTENSITY          uint32 = 0x00000008
	LASZIP_DECOMPRESS_SELECTIVE_SCAN_ANGLE         uint32 = 0x00000010
	LASZIP_DECOMPRESS_SELECTIVE_USER_DATA          uint32 = 0x00000020
	LASZIP_DECOMPRESS_SELECTIVE_POINT_SOURCE       uint32 = 0x00000040
	LASZIP_DECOMPRESS_SELECTIVE_GPS_TIME           uint32 = 0x00000080
	LASZIP_DECOMPRESS_SELECTIVE_RGB                uint32 = 0x00000100
	LASZIP_DECOMPRESS_SELECTIVE_NIR                uint32 = 0x00000200
	LASZIP_DECOMPRESS_SELECTIVE_WAVEPACKET         uint32 = 0x00000400
	LASZIP_DECOMPRESS_SELECTIVE_BYTE0              uint32 = 0x00010000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE1              uint32 = 0x00020000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE2              uint32 = 0x00040000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE3              uint32 = 0x00080000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE4              uint32 = 0x00100000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE5              uint32 = 0x00200000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE6              uint32 = 0x00400000
	LASZIP_DECOMPRESS_SELECTIVE_BYTE7              uint32 = 0x00800000
	LASZIP_DECOMPRESS_SELECTIVE_EXTRA_BYTES        uint32 = 0xFFFF0000
)
