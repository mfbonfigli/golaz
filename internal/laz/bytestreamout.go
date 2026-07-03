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
// bytestreamout.go — ByteStreamOut interface ported from
// src/bytestreamout.hpp. This is the output mirror of ByteStreamIn.
// The C++ base class also carries a putBits/flushBits bit buffer, but the
// LASzip writer core never uses it, so it is intentionally not ported.
package laz

// ByteStreamOut is the abstract output stream interface for writing raw
// bytes and multi-byte little-endian values. Like the reader side, only the
// little-endian methods are ported (LASzip operates on little-endian data;
// the C++ BE host variants were dropped).
type ByteStreamOut interface {
	// PutByte writes a single byte.
	PutByte(b byte) error

	// PutBytes writes all bytes in buf.
	PutBytes(buf []byte) error

	// Put16bitsLE writes buf[0:2] as a 16-bit little-endian field.
	Put16bitsLE(buf []byte) error

	// Put32bitsLE writes buf[0:4] as a 32-bit little-endian field.
	Put32bitsLE(buf []byte) error

	// Put64bitsLE writes buf[0:8] as a 64-bit little-endian field.
	Put64bitsLE(buf []byte) error

	// IsSeekable reports whether the stream supports seeking.
	IsSeekable() bool

	// Tell returns the current position in the stream.
	Tell() (int64, error)

	// Seek moves the stream position to the given absolute offset.
	Seek(position int64) error

	// SeekEnd moves the stream position to distance bytes before the end.
	SeekEnd(distance int64) error
}
