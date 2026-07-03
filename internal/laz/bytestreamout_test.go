// Copyright (c) 2026 Massimo Federico Bonfigli
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

package laz

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ===========================================================================
// ByteStreamOutArray tests
// ===========================================================================

func TestByteStreamOutArrayBasicWrites(t *testing.T) {
	s := NewByteStreamOutArray()

	if !s.IsSeekable() {
		t.Error("array stream should be seekable")
	}
	if err := s.PutByte(0x01); err != nil {
		t.Fatalf("PutByte: %v", err)
	}
	if err := s.PutBytes([]byte{0x02, 0x03, 0x04}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if err := s.Put16bitsLE([]byte{0x05, 0x06}); err != nil {
		t.Fatalf("Put16bitsLE: %v", err)
	}
	if err := s.Put32bitsLE([]byte{0x07, 0x08, 0x09, 0x0A}); err != nil {
		t.Fatalf("Put32bitsLE: %v", err)
	}
	if err := s.Put64bitsLE([]byte{0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12}); err != nil {
		t.Fatalf("Put64bitsLE: %v", err)
	}

	want := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09,
		0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12,
	}
	if !bytes.Equal(s.GetData(), want) {
		t.Errorf("GetData() = % x, want % x", s.GetData(), want)
	}
	if s.GetCurr() != int64(len(want)) {
		t.Errorf("GetCurr() = %d, want %d", s.GetCurr(), len(want))
	}
	if s.GetSize() != int64(len(want)) {
		t.Errorf("GetSize() = %d, want %d", s.GetSize(), len(want))
	}
	pos, err := s.Tell()
	if err != nil {
		t.Fatalf("Tell: %v", err)
	}
	if pos != int64(len(want)) {
		t.Errorf("Tell() = %d, want %d", pos, len(want))
	}
}

func TestByteStreamOutArraySeekZeroRewrite(t *testing.T) {
	// Mirrors the C++ writer's per-chunk seek(0)+getCurr reuse pattern:
	// after Seek(0) the stream overwrites in place, GetCurr tracks the
	// write position, and only [0:curr] is the produced output.
	s := NewByteStreamOutArray()
	if err := s.PutBytes([]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}

	if err := s.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	if s.GetCurr() != 0 {
		t.Fatalf("GetCurr() after Seek(0) = %d, want 0", s.GetCurr())
	}

	// Overwrite the first three bytes in place.
	if err := s.PutBytes([]byte{0x11, 0x22}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if err := s.PutByte(0x33); err != nil {
		t.Fatalf("PutByte: %v", err)
	}
	if s.GetCurr() != 3 {
		t.Errorf("GetCurr() = %d, want 3", s.GetCurr())
	}
	// Size (high-water mark) is unchanged; bytes beyond curr are preserved.
	if s.GetSize() != 5 {
		t.Errorf("GetSize() = %d, want 5", s.GetSize())
	}
	want := []byte{0x11, 0x22, 0x33, 0xDD, 0xEE}
	if !bytes.Equal(s.GetData(), want) {
		t.Errorf("GetData() = % x, want % x", s.GetData(), want)
	}
	// The produced output for the writer's usage pattern is [0:curr].
	if !bytes.Equal(s.GetData()[:s.GetCurr()], []byte{0x11, 0x22, 0x33}) {
		t.Errorf("produced output = % x, want 11 22 33", s.GetData()[:s.GetCurr()])
	}

	// Writing past the high-water mark grows the buffer again.
	if err := s.PutBytes([]byte{0x44, 0x55, 0x66}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	want = []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	if !bytes.Equal(s.GetData(), want) {
		t.Errorf("GetData() = % x, want % x", s.GetData(), want)
	}
	if s.GetSize() != 6 || s.GetCurr() != 6 {
		t.Errorf("GetSize()/GetCurr() = %d/%d, want 6/6", s.GetSize(), s.GetCurr())
	}
}

func TestByteStreamOutArraySeekBounds(t *testing.T) {
	s := NewByteStreamOutArray()
	if err := s.PutBytes([]byte{1, 2, 3}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}

	if err := s.Seek(-1); err == nil {
		t.Error("Seek(-1) should fail")
	}
	if err := s.Seek(4); err == nil {
		t.Error("Seek(4) beyond size should fail")
	}
	if err := s.Seek(3); err != nil {
		t.Errorf("Seek(3) at size should succeed: %v", err)
	}
	if err := s.SeekEnd(0); err != nil {
		t.Errorf("SeekEnd(0): %v", err)
	}
	if s.GetCurr() != 3 {
		t.Errorf("GetCurr() after SeekEnd(0) = %d, want 3", s.GetCurr())
	}
	if err := s.SeekEnd(2); err != nil {
		t.Errorf("SeekEnd(2): %v", err)
	}
	if s.GetCurr() != 1 {
		t.Errorf("GetCurr() after SeekEnd(2) = %d, want 1", s.GetCurr())
	}
	if err := s.SeekEnd(4); err == nil {
		t.Error("SeekEnd(4) beyond start should fail")
	}
}

func TestByteStreamOutArrayReset(t *testing.T) {
	s := NewByteStreamOutArray()
	if err := s.PutBytes([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	s.Reset()
	if s.GetCurr() != 0 || s.GetSize() != 0 || len(s.GetData()) != 0 {
		t.Errorf("after Reset: curr=%d size=%d len(data)=%d, want all 0",
			s.GetCurr(), s.GetSize(), len(s.GetData()))
	}
	if err := s.PutBytes([]byte{9, 8}); err != nil {
		t.Fatalf("PutBytes after Reset: %v", err)
	}
	if !bytes.Equal(s.GetData(), []byte{9, 8}) {
		t.Errorf("GetData() after Reset+write = % x, want 09 08", s.GetData())
	}
}

// ===========================================================================
// ByteStreamOutWriter tests
// ===========================================================================

func TestByteStreamOutWriter(t *testing.T) {
	var buf bytes.Buffer
	s := NewByteStreamOutWriter(&buf)

	if s.IsSeekable() {
		t.Error("writer stream should not be seekable")
	}
	pos, err := s.Tell()
	if err != nil || pos != 0 {
		t.Errorf("Tell() = %d, %v; want 0, nil", pos, err)
	}

	if err := s.PutByte(0x01); err != nil {
		t.Fatalf("PutByte: %v", err)
	}
	if err := s.PutBytes([]byte{0x02, 0x03}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	if err := s.Put32bitsLE([]byte{0x04, 0x05, 0x06, 0x07}); err != nil {
		t.Fatalf("Put32bitsLE: %v", err)
	}

	pos, err = s.Tell()
	if err != nil || pos != 7 {
		t.Errorf("Tell() = %d, %v; want 7, nil", pos, err)
	}
	if !bytes.Equal(buf.Bytes(), []byte{1, 2, 3, 4, 5, 6, 7}) {
		t.Errorf("written bytes = % x", buf.Bytes())
	}

	if err := s.Seek(0); err == nil {
		t.Error("Seek should fail on non-seekable stream")
	}
	if err := s.SeekEnd(0); err == nil {
		t.Error("SeekEnd should fail on non-seekable stream")
	}
}

// ===========================================================================
// ByteStreamOutFile tests
// ===========================================================================

func TestByteStreamOutFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	s := NewByteStreamOutFile(f)
	if !s.IsSeekable() {
		t.Error("file stream should be seekable")
	}

	if err := s.PutBytes([]byte{0xAA, 0xBB, 0xCC, 0xDD}); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	pos, err := s.Tell()
	if err != nil || pos != 4 {
		t.Fatalf("Tell() = %d, %v; want 4, nil", pos, err)
	}

	// Seek back and patch (the chunk-table offset slot pattern).
	if err := s.Seek(1); err != nil {
		t.Fatalf("Seek(1): %v", err)
	}
	if err := s.PutByte(0x00); err != nil {
		t.Fatalf("PutByte: %v", err)
	}
	if err := s.SeekEnd(0); err != nil {
		t.Fatalf("SeekEnd(0): %v", err)
	}
	pos, err = s.Tell()
	if err != nil || pos != 4 {
		t.Fatalf("Tell() after SeekEnd(0) = %d, %v; want 4, nil", pos, err)
	}
	if err := s.PutByte(0xEE); err != nil {
		t.Fatalf("PutByte: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	want := []byte{0xAA, 0x00, 0xCC, 0xDD, 0xEE}
	if !bytes.Equal(got, want) {
		t.Errorf("file contents = % x, want % x", got, want)
	}
}
