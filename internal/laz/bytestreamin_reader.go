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
// bytestreamin_reader.go — io.Reader/io.ReadSeeker-backed ByteStreamIn
// ported from src/bytestreamin_istream.hpp.
package laz

import (
	"bufio"
	"fmt"
	"io"
)

// ByteStreamInReader implements ByteStreamIn backed by an io.Reader.
// Seekability depends on whether the underlying reader implements io.ReadSeeker.
//
// All reads go through a bufio.Reader (64 KiB) for the same reason as
// ByteStreamInFile: the arithmetic decoder issues many 1-byte GetByte
// calls for the LAS 1.2/1.3 pointwise compressor path, and batching
// those via a user-space buffer eliminates the per-byte syscall overhead.
//
// When the stream is seekable (underlying implements io.ReadSeeker),
// Seek/SeekEnd flush the lookahead via br.Reset and Tell() subtracts
// the buffered-but-unconsumed bytes from the kernel position.
type ByteStreamInReader struct {
	r    io.Reader
	rs   io.ReadSeeker // non-nil if underlying supports seeking
	br   *bufio.Reader
	bits bitBuffer
}

// NewByteStreamInReader creates a new ByteStreamInReader from an io.Reader.
// The stream will not be seekable unless the reader also implements io.ReadSeeker.
func NewByteStreamInReader(r io.Reader) *ByteStreamInReader {
	s := &ByteStreamInReader{r: r, br: bufio.NewReaderSize(r, 1<<16)}
	if rs, ok := r.(io.ReadSeeker); ok {
		s.rs = rs
	}
	return s
}

func (s *ByteStreamInReader) GetByte() (byte, error) {
	b, err := s.br.ReadByte()
	if err != nil {
		return 0, fmt.Errorf("getByte: %w", err)
	}
	return b, nil
}

func (s *ByteStreamInReader) GetBytes(buf []byte) error {
	if _, err := io.ReadFull(s.br, buf); err != nil {
		return fmt.Errorf("getBytes: %w", err)
	}
	return nil
}

func (s *ByteStreamInReader) Get16bitsLE(buf []byte) error {
	return s.GetBytes(buf[:2])
}

func (s *ByteStreamInReader) Get32bitsLE(buf []byte) error {
	return s.GetBytes(buf[:4])
}

func (s *ByteStreamInReader) Get64bitsLE(buf []byte) error {
	return s.GetBytes(buf[:8])
}

func (s *ByteStreamInReader) Get16bitsBE(buf []byte) error {
	var tmp [2]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1] = tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInReader) Get32bitsBE(buf []byte) error {
	var tmp [4]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3] = tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInReader) Get64bitsBE(buf []byte) error {
	var tmp [8]byte
	if err := s.GetBytes(tmp[:]); err != nil {
		return err
	}
	buf[0], buf[1], buf[2], buf[3], buf[4], buf[5], buf[6], buf[7] =
		tmp[7], tmp[6], tmp[5], tmp[4], tmp[3], tmp[2], tmp[1], tmp[0]
	return nil
}

func (s *ByteStreamInReader) IsSeekable() bool {
	return s.rs != nil
}

// Tell returns the logical read position (kernel position minus buffered lookahead).
func (s *ByteStreamInReader) Tell() (int64, error) {
	if s.rs == nil {
		return 0, fmt.Errorf("tell: stream is not seekable")
	}
	pos, err := s.rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	return pos - int64(s.br.Buffered()), nil
}

// Seek moves to an absolute position and discards the lookahead buffer.
func (s *ByteStreamInReader) Seek(position int64) error {
	if s.rs == nil {
		return fmt.Errorf("seek: stream is not seekable")
	}
	_, err := s.rs.Seek(position, io.SeekStart)
	if err != nil {
		return err
	}
	s.br.Reset(s.r)
	return nil
}

// SeekEnd moves to (size - distance) and discards the lookahead buffer.
func (s *ByteStreamInReader) SeekEnd(distance int64) error {
	if s.rs == nil {
		return fmt.Errorf("seekEnd: stream is not seekable")
	}
	_, err := s.rs.Seek(-distance, io.SeekEnd)
	if err != nil {
		return err
	}
	s.br.Reset(s.r)
	return nil
}

func (s *ByteStreamInReader) SkipBytes(numBytes uint32) error {
	if s.rs != nil {
		pos, err := s.Tell()
		if err != nil {
			return err
		}
		return s.Seek(pos + int64(numBytes))
	}
	// Non-seekable: discard via bufio (avoids an allocation; Discard reads
	// from the buffer and only calls the underlying reader if needed).
	_, err := s.br.Discard(int(numBytes))
	return err
}

func (s *ByteStreamInReader) GetBits(numBits uint32) (uint32, error) {
	return s.bits.getBits(s.Get32bitsLE, numBits)
}
