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
// bytestreamout_file.go — io.WriteSeeker-backed ByteStreamOut ported from
// src/bytestreamout_file.hpp. Provides LE-only write methods since LASzip
// operates on little-endian data.
package laz

import (
	"fmt"
	"io"
)

// ByteStreamOutFile implements ByteStreamOut backed by an io.WriteSeeker
// (typically an *os.File). The stream is seekable, which the writer uses
// to patch the chunk-table offset slot on close.
//
// Writes are not buffered: the arithmetic encoder accumulates its whole
// chunk production internally and flushes it in a single PutBytes call,
// so per-byte writes are rare (raw first points, headers, table fields).
type ByteStreamOutFile struct {
	ws io.WriteSeeker
}

// NewByteStreamOutFile creates a new ByteStreamOutFile from an io.WriteSeeker.
func NewByteStreamOutFile(ws io.WriteSeeker) *ByteStreamOutFile {
	return &ByteStreamOutFile{ws: ws}
}

func (s *ByteStreamOutFile) PutByte(b byte) error {
	buf := [1]byte{b}
	return s.PutBytes(buf[:])
}

func (s *ByteStreamOutFile) PutBytes(buf []byte) error {
	if _, err := s.ws.Write(buf); err != nil {
		return fmt.Errorf("putBytes: %w", err)
	}
	return nil
}

func (s *ByteStreamOutFile) Put16bitsLE(buf []byte) error {
	return s.PutBytes(buf[:2])
}

func (s *ByteStreamOutFile) Put32bitsLE(buf []byte) error {
	return s.PutBytes(buf[:4])
}

func (s *ByteStreamOutFile) Put64bitsLE(buf []byte) error {
	return s.PutBytes(buf[:8])
}

func (s *ByteStreamOutFile) IsSeekable() bool {
	return true
}

func (s *ByteStreamOutFile) Tell() (int64, error) {
	return s.ws.Seek(0, io.SeekCurrent)
}

func (s *ByteStreamOutFile) Seek(position int64) error {
	if _, err := s.ws.Seek(position, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	return nil
}

func (s *ByteStreamOutFile) SeekEnd(distance int64) error {
	if _, err := s.ws.Seek(-distance, io.SeekEnd); err != nil {
		return fmt.Errorf("seekEnd: %w", err)
	}
	return nil
}
