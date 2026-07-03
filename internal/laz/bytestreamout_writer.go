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
// bytestreamout_writer.go — io.Writer-backed ByteStreamOut ported from
// src/bytestreamout_ostream.hpp (the non-seekable pipe/stdout path).
package laz

import (
	"fmt"
	"io"
)

// ByteStreamOutWriter implements ByteStreamOut backed by a plain io.Writer.
// The stream is not seekable: Tell is tracked by counting written bytes,
// and Seek/SeekEnd return errors. The writer uses this for the non-seekable
// output path, where the chunk-table offset slot cannot be patched and the
// real table position is appended after the table instead.
type ByteStreamOutWriter struct {
	w   io.Writer
	pos int64 // number of bytes written so far
}

// NewByteStreamOutWriter creates a new ByteStreamOutWriter from an io.Writer.
func NewByteStreamOutWriter(w io.Writer) *ByteStreamOutWriter {
	return &ByteStreamOutWriter{w: w}
}

func (s *ByteStreamOutWriter) PutByte(b byte) error {
	buf := [1]byte{b}
	return s.PutBytes(buf[:])
}

func (s *ByteStreamOutWriter) PutBytes(buf []byte) error {
	n, err := s.w.Write(buf)
	s.pos += int64(n)
	if err != nil {
		return fmt.Errorf("putBytes: %w", err)
	}
	return nil
}

func (s *ByteStreamOutWriter) Put16bitsLE(buf []byte) error {
	return s.PutBytes(buf[:2])
}

func (s *ByteStreamOutWriter) Put32bitsLE(buf []byte) error {
	return s.PutBytes(buf[:4])
}

func (s *ByteStreamOutWriter) Put64bitsLE(buf []byte) error {
	return s.PutBytes(buf[:8])
}

func (s *ByteStreamOutWriter) IsSeekable() bool {
	return false
}

// Tell returns the number of bytes written so far.
func (s *ByteStreamOutWriter) Tell() (int64, error) {
	return s.pos, nil
}

func (s *ByteStreamOutWriter) Seek(position int64) error {
	return fmt.Errorf("seek: stream is not seekable")
}

func (s *ByteStreamOutWriter) SeekEnd(distance int64) error {
	return fmt.Errorf("seekEnd: stream is not seekable")
}
