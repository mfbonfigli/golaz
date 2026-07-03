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
// bytestreamin_array.go — []byte-backed ByteStreamIn ported from
// src/bytestreamin_array.hpp. Used for layered v3/v4 decompression where
// each layer gets its own byte stream carved from a chunk.
package laz

import (
	"fmt"
	"io"
)

// ByteStreamInArray implements ByteStreamIn backed by an in-memory []byte slice.
type ByteStreamInArray struct {
	data []byte
	pos  int64 // current read position (C++: curr)
	bits bitBuffer
}

// NewByteStreamInArray creates a new ByteStreamInArray from a byte slice.
func NewByteStreamInArray(data []byte) *ByteStreamInArray {
	return &ByteStreamInArray{data: data}
}

// Init reinitializes the stream with new data, resetting position to 0.
func (s *ByteStreamInArray) Init(data []byte) {
	s.data = data
	s.pos = 0
	s.bits = bitBuffer{}
}

func (s *ByteStreamInArray) GetByte() (byte, error) {
	if s.pos >= int64(len(s.data)) {
		// Wraps io.EOF so over-reads classify as end-of-file, matching the
		// C++ ByteStreamInArray which throws EOF.
		return 0, fmt.Errorf("getByte: %w", io.EOF)
	}
	b := s.data[s.pos]
	s.pos++
	return b, nil
}

func (s *ByteStreamInArray) GetBytes(buf []byte) error {
	n := len(buf)
	if s.pos+int64(n) > int64(len(s.data)) {
		return fmt.Errorf("getBytes: at %d, need %d, have %d: %w", s.pos, n, len(s.data), io.ErrUnexpectedEOF)
	}
	copy(buf, s.data[s.pos:s.pos+int64(n)])
	s.pos += int64(n)
	return nil
}

func (s *ByteStreamInArray) Get16bitsLE(buf []byte) error {
	return s.GetBytes(buf[:2])
}

func (s *ByteStreamInArray) Get32bitsLE(buf []byte) error {
	return s.GetBytes(buf[:4])
}

func (s *ByteStreamInArray) Get64bitsLE(buf []byte) error {
	return s.GetBytes(buf[:8])
}

func (s *ByteStreamInArray) Get16bitsBE(buf []byte) error {
	var tmp [2]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1] = tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInArray) Get32bitsBE(buf []byte) error {
	var tmp [4]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3] = tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInArray) Get64bitsBE(buf []byte) error {
	var tmp [8]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7] =
		tmp[7], tmp[6], tmp[5], tmp[4], tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInArray) IsSeekable() bool {
	return true
}

func (s *ByteStreamInArray) Tell() (int64, error) {
	return s.pos, nil
}

func (s *ByteStreamInArray) Seek(position int64) error {
	if position < 0 || position > int64(len(s.data)) {
		return fmt.Errorf("seek: position %d out of range [0, %d]", position, len(s.data))
	}
	s.pos = position
	return nil
}

func (s *ByteStreamInArray) SeekEnd(distance int64) error {
	newPos := int64(len(s.data)) - distance
	if newPos < 0 || newPos > int64(len(s.data)) {
		return fmt.Errorf("seekEnd: distance %d out of range", distance)
	}
	s.pos = newPos
	return nil
}

func (s *ByteStreamInArray) SkipBytes(numBytes uint32) error {
	newPos := s.pos + int64(numBytes)
	if newPos > int64(len(s.data)) {
		return fmt.Errorf("skipBytes: cannot skip %d from %d (length %d)", numBytes, s.pos, len(s.data))
	}
	s.pos = newPos
	return nil
}

func (s *ByteStreamInArray) GetBits(numBits uint32) (uint32, error) {
	return s.bits.getBits(s.Get32bitsLE, numBits)
}
