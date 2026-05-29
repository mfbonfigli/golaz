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
// bytestreamin_file.go — *os.File-backed ByteStreamIn ported from
// src/bytestreamin_file.hpp. Provides LE-only read methods since LASzip
// operates on little-endian data.
package laz

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// ByteStreamInFile implements ByteStreamIn backed by an *os.File.
//
// All reads go through a bufio.Reader (default 64 KiB) so that the
// arithmetic decoder's many 1-byte GetByte calls are served from a
// user-space buffer rather than issuing a syscall each time.  This is
// especially important for LAS 1.2/1.3 (pointwise compressor) where
// renormDecInterval pulls one byte at a time straight from this stream.
//
// Seek/Tell correctness: after any seek we call br.Reset(f) to discard
// the lookahead; Tell() subtracts the number of buffered-but-unconsumed
// bytes from the kernel file position.
type ByteStreamInFile struct {
	f    *os.File
	br   *bufio.Reader
	bits bitBuffer
}

// NewByteStreamInFile creates a new ByteStreamInFile from an *os.File.
func NewByteStreamInFile(f *os.File) *ByteStreamInFile {
	return &ByteStreamInFile{f: f, br: bufio.NewReaderSize(f, 1<<16)}
}

func (s *ByteStreamInFile) GetByte() (byte, error) {
	b, err := s.br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("getByte: %w", err)
	}
	return b, nil
}

func (s *ByteStreamInFile) GetBytes(buf []byte) error {
	if _, err := io.ReadFull(s.br, buf); err != nil {
		return fmt.Errorf("getBytes: %w", err)
	}
	return nil
}

func (s *ByteStreamInFile) Get16bitsLE(buf []byte) error {
	return s.GetBytes(buf[:2])
}

func (s *ByteStreamInFile) Get32bitsLE(buf []byte) error {
	return s.GetBytes(buf[:4])
}

func (s *ByteStreamInFile) Get64bitsLE(buf []byte) error {
	return s.GetBytes(buf[:8])
}

func (s *ByteStreamInFile) Get16bitsBE(buf []byte) error {
	var tmp [2]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1] = tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInFile) Get32bitsBE(buf []byte) error {
	var tmp [4]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3] = tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInFile) Get64bitsBE(buf []byte) error {
	var tmp [8]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7] =
		tmp[7], tmp[6], tmp[5], tmp[4], tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInFile) IsSeekable() bool {
	return true
}

// Tell returns the logical read position (kernel position minus
// any bytes already read into the bufio lookahead buffer).
func (s *ByteStreamInFile) Tell() (int64, error) {
	pos, err := s.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	return pos - int64(s.br.Buffered()), nil
}

// Seek moves to an absolute byte position and discards the lookahead buffer.
func (s *ByteStreamInFile) Seek(position int64) error {
	_, err := s.f.Seek(position, io.SeekStart)
	if err != nil {
		return err
	}
	s.br.Reset(s.f)
	return nil
}

// SeekEnd moves to (fileSize - distance) and discards the lookahead buffer.
func (s *ByteStreamInFile) SeekEnd(distance int64) error {
	_, err := s.f.Seek(-distance, io.SeekEnd)
	if err != nil {
		return err
	}
	s.br.Reset(s.f)
	return nil
}

func (s *ByteStreamInFile) SkipBytes(numBytes uint32) error {
	pos, err := s.Tell()
	if err != nil {
		return err
	}
	return s.Seek(pos + int64(numBytes))
}

func (s *ByteStreamInFile) GetBits(numBits uint32) (uint32, error) {
	return s.bits.getBits(s.Get32bitsLE, numBits)
}
