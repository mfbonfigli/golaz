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
// common_v1.go — v1 shared types (LASwavepacket13) ported from
// src/laszip_common_v1.hpp. Read-relevant subset only.
package laz

import (
	"encoding/binary"
	"math"
)

// LASwavepacket13 represents a LAS 1.3/1.4 wave packet.
// It matches the 29-byte on-disk layout of a WAVEPACKET13.
type LASwavepacket13 struct {
	Offset      uint64
	PacketSize  uint32
	ReturnPoint float32
	X           float32
	Y           float32
	Z           float32
}

// UnpackLASwavepacket13 decodes a LASwavepacket13 from raw 29-byte LE data.
func UnpackLASwavepacket13(item []byte) LASwavepacket13 {
	var wp LASwavepacket13
	wp.Offset = binary.LittleEndian.Uint64(item[0:8])
	wp.PacketSize = binary.LittleEndian.Uint32(item[8:12])
	wp.ReturnPoint = math.Float32frombits(binary.LittleEndian.Uint32(item[12:16]))
	wp.X = math.Float32frombits(binary.LittleEndian.Uint32(item[16:20]))
	wp.Y = math.Float32frombits(binary.LittleEndian.Uint32(item[20:24]))
	wp.Z = math.Float32frombits(binary.LittleEndian.Uint32(item[24:28]))
	return wp
}

// PackLASwavepacket13 encodes a LASwavepacket13 into raw 29-byte LE data.
// (Only needed for write path but included for completeness.)
func PackLASwavepacket13(wp *LASwavepacket13) []byte {
	item := make([]byte, 29)
	binary.LittleEndian.PutUint64(item[0:8], wp.Offset)
	binary.LittleEndian.PutUint32(item[8:12], wp.PacketSize)
	binary.LittleEndian.PutUint32(item[12:16], math.Float32bits(wp.ReturnPoint))
	binary.LittleEndian.PutUint32(item[16:20], math.Float32bits(wp.X))
	binary.LittleEndian.PutUint32(item[20:24], math.Float32bits(wp.Y))
	binary.LittleEndian.PutUint32(item[24:28], math.Float32bits(wp.Z))
	// byte 28 remains 0 (C++ struct packs U64 + U32 + 5×U32 = 8+4+20 = 32, but on-disk is 29 bytes:
	// 8+4+4+4+4+4 = 28; C++ uses [29] for WAVEPACKET, the last byte is never written but reserved)
	return item
}
