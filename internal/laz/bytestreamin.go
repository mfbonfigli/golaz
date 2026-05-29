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
// bytestreamin.go — ByteStreamIn interface and bit-buffer logic ported from
// src/bytestreamin.hpp. The getBits() method replicates the exact bit-buffer
// algorithm of ByteStreamIn::getBits() for arithmetic decoding.
package laz

import (
	"encoding/binary"
	"fmt"
)

// ByteStreamIn is the abstract input stream interface for reading raw bytes
// and multi-byte values in little-endian or big-endian order.
type ByteStreamIn interface {
	// GetByte reads and returns a single byte.
	GetByte() (byte, error)

	// GetBytes reads len(buf) bytes into buf.
	GetBytes(buf []byte) error

	// Get16bitsLE reads 2 bytes in little-endian order into buf[0:2].
	Get16bitsLE(buf []byte) error

	// Get32bitsLE reads 4 bytes in little-endian order into buf[0:4].
	Get32bitsLE(buf []byte) error

	// Get64bitsLE reads 8 bytes in little-endian order into buf[0:8].
	Get64bitsLE(buf []byte) error

	// Get16bitsBE reads 2 bytes in big-endian order into buf[0:2].
	Get16bitsBE(buf []byte) error

	// Get32bitsBE reads 4 bytes in big-endian order into buf[0:4].
	Get32bitsBE(buf []byte) error

	// Get64bitsBE reads 8 bytes in big-endian order into buf[0:8].
	Get64bitsBE(buf []byte) error

	// IsSeekable reports whether the stream supports seeking.
	IsSeekable() bool

	// Tell returns the current position in the stream.
	Tell() (int64, error)

	// Seek moves the stream position to the given absolute offset.
	Seek(position int64) error

	// SeekEnd moves the stream position to distance bytes before the end.
	SeekEnd(distance int64) error

	// SkipBytes advances the stream by numBytes.
	SkipBytes(numBytes uint32) error

	// GetBits reads and returns numBits bits from the stream.
	// This is the core bit-level primitive used by ArithmeticDecoder.
	GetBits(numBits uint32) (uint32, error)
}

// bitBuffer holds the bit-level buffering state used by GetBits.
// Embed *bitBuffer in concrete ByteStreamIn implementations and delegate
// GetBits to bitBuffer.getBits().
type bitBuffer struct {
	buffer uint64 // accumulated bits (C++: bit_buffer)
	num    uint32 // number of bits available in buffer (C++: num_buffer)
}

// getBits reads numBits from the underlying stream via get32LE and returns
// them as a uint32. This replicates ByteStreamIn::getBits() exactly.
//
//	get32LE: function that reads 4 LE bytes into buf from the concrete stream.
func (b *bitBuffer) getBits(get32LE func(buf []byte) error, numBits uint32) (uint32, error) {
	if numBits == 0 {
		return 0, nil
	}
	var buf [4]byte
	for b.num < numBits {
		if err := get32LE(buf[:]); err != nil {
			return 0, fmt.Errorf("getBits: %w", err)
		}
		input := binary.LittleEndian.Uint32(buf[:])
		b.buffer |= uint64(input) << b.num
		b.num += 32
	}
	// Extract the low numBits bits.
	mask := (uint64(1) << numBits) - 1
	result := uint32(b.buffer & mask)
	b.buffer >>= numBits
	b.num -= numBits
	return result, nil
}
