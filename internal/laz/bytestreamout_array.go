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
// bytestreamout_array.go — []byte-backed ByteStreamOut ported from
// src/bytestreamout_array.hpp. Used by the layered v3/v4 compression path
// where each layer accumulates its bytes in memory before the chunk is
// written out to the main stream.
package laz

import "fmt"

// ByteStreamOutArray implements ByteStreamOut backed by an in-memory,
// growing []byte slice.
//
// Semantics ported from the C++ ByteStreamOutArray: the stream tracks a
// write cursor (curr) and a high-water mark (size == len(data)). After
// Seek(0) writes overwrite in place and GetCurr() tracks the write cursor;
// for the writer's usage pattern only data[0:curr] is the produced output.
// The C++ writer reuses a layer stream across chunks via seek(0)+getCurr;
// the Go writer instead calls Reset() per chunk, which truncates the buffer
// to empty — behaviorally identical for this usage since only [0:curr] is
// ever consumed.
type ByteStreamOutArray struct {
	data []byte // written bytes; len(data) is the C++ "size"
	curr int64  // current write position (C++: curr)
}

// NewByteStreamOutArray creates a new empty ByteStreamOutArray.
func NewByteStreamOutArray() *ByteStreamOutArray {
	return &ByteStreamOutArray{}
}

// Reset truncates the stream to empty and rewinds the write cursor.
// The underlying allocation is retained for reuse across chunks.
func (s *ByteStreamOutArray) Reset() {
	s.data = s.data[:0]
	s.curr = 0
}

// GetData returns the full backing buffer (the C++ getData(), whose length
// is the C++ getSize()). For the writer's usage pattern only [0:GetCurr()]
// is the produced output.
func (s *ByteStreamOutArray) GetData() []byte {
	return s.data
}

// GetSize returns the high-water mark of the stream (C++: getSize()).
func (s *ByteStreamOutArray) GetSize() int64 {
	return int64(len(s.data))
}

// GetCurr returns the current write position (C++: getCurr()).
func (s *ByteStreamOutArray) GetCurr() int64 {
	return s.curr
}

func (s *ByteStreamOutArray) PutByte(b byte) error {
	if s.curr == int64(len(s.data)) {
		s.data = append(s.data, b)
	} else {
		s.data[s.curr] = b
	}
	s.curr++
	return nil
}

func (s *ByteStreamOutArray) PutBytes(buf []byte) error {
	end := s.curr + int64(len(buf))
	if grow := end - int64(len(s.data)); grow > 0 {
		s.data = append(s.data, make([]byte, grow)...)
	}
	copy(s.data[s.curr:end], buf)
	s.curr = end
	return nil
}

func (s *ByteStreamOutArray) Put16bitsLE(buf []byte) error {
	return s.PutBytes(buf[:2])
}

func (s *ByteStreamOutArray) Put32bitsLE(buf []byte) error {
	return s.PutBytes(buf[:4])
}

func (s *ByteStreamOutArray) Put64bitsLE(buf []byte) error {
	return s.PutBytes(buf[:8])
}

func (s *ByteStreamOutArray) IsSeekable() bool {
	return true
}

func (s *ByteStreamOutArray) Tell() (int64, error) {
	return s.curr, nil
}

func (s *ByteStreamOutArray) Seek(position int64) error {
	if position < 0 || position > int64(len(s.data)) {
		return fmt.Errorf("seek: position %d out of range [0, %d]", position, len(s.data))
	}
	s.curr = position
	return nil
}

func (s *ByteStreamOutArray) SeekEnd(distance int64) error {
	newPos := int64(len(s.data)) - distance
	if newPos < 0 || newPos > int64(len(s.data)) {
		return fmt.Errorf("seekEnd: distance %d out of range", distance)
	}
	s.curr = newPos
	return nil
}
