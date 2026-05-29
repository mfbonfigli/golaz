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
	"encoding/binary"
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// bitBuffer.getBits tests — core arithmetic decoder primitive
// ---------------------------------------------------------------------------

func TestBitBufferGetBits_ExactZero(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	stream := NewByteStreamInArray(data)

	// Read 0 bits should return 0 without consuming anything.
	val, err := stream.GetBits(0)
	if err != nil {
		t.Fatalf("GetBits(0) error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(0) = %d, want 0", val)
	}
	pos, _ := stream.Tell()
	if pos != 0 {
		t.Fatalf("GetBits(0) consumed %d bytes, want 0", pos)
	}
}

func TestBitBufferGetBits_SingleBit(t *testing.T) {
	//                               bit layout:  1 0 1 1 0 0 1 0  ...
	data := []byte{0x4D, 0x00, 0x00, 0x00} // 0x4D = 01001101 binary
	stream := NewByteStreamInArray(data)

	// Read bits LSB-first (matching C++ getBits behavior):
	// bit 0 (LSB) of 0x4D: 1
	val, err := stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) error: %v", err)
	}
	if val != 1 {
		t.Fatalf("GetBits(1) = %d, want 1", val)
	}

	// bit 1: 0
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #2 error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(1) #2 = %d, want 0", val)
	}

	// bit 2: 1
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #3 error: %v", err)
	}
	if val != 1 {
		t.Fatalf("GetBits(1) #3 = %d, want 1", val)
	}

	// bit 3: 1
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #4 error: %v", err)
	}
	if val != 1 {
		t.Fatalf("GetBits(1) #4 = %d, want 1", val)
	}

	// bit 4: 0
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #5 error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(1) #5 = %d, want 0", val)
	}

	// bit 5: 0
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #6 error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(1) #6 = %d, want 0", val)
	}

	// bit 6: 1
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #7 error: %v", err)
	}
	if val != 1 {
		t.Fatalf("GetBits(1) #7 = %d, want 1", val)
	}

	// bit 7 (MSB): 0
	val, err = stream.GetBits(1)
	if err != nil {
		t.Fatalf("GetBits(1) #8 error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(1) #8 = %d, want 0", val)
	}
}

func TestBitBufferGetBits_MultiBit(t *testing.T) {
	// Two 32-bit words: 0x00000005 (bits: ...0101), then 0x00000000
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], 5) // 5 = 101 binary
	binary.LittleEndian.PutUint32(data[4:8], 0)

	stream := NewByteStreamInArray(data)

	// Read 3 bits: should be 101 = 5
	val, err := stream.GetBits(3)
	if err != nil {
		t.Fatalf("GetBits(3) error: %v", err)
	}
	if val != 5 {
		t.Fatalf("GetBits(3) = %d, want 5", val)
	}

	// 29 bits remaining in first word (all zero) + 32 bits from second = should give 0
	val, err = stream.GetBits(4)
	if err != nil {
		t.Fatalf("GetBits(4) error: %v", err)
	}
	if val != 0 {
		t.Fatalf("GetBits(4) = %d, want 0", val)
	}
}

func TestBitBufferGetBits_32BitsExact(t *testing.T) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 0xDEADBEEF)
	stream := NewByteStreamInArray(data)

	val, err := stream.GetBits(32)
	if err != nil {
		t.Fatalf("GetBits(32) error: %v", err)
	}
	if val != 0xDEADBEEF {
		t.Fatalf("GetBits(32) = %#08x, want 0xDEADBEEF", val)
	}
}

func TestBitBufferGetBits_AcrossWordBoundary(t *testing.T) {
	// First word: 0xFFFFFF (24 bits of 1s), then 0x05 in second word
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data[0:4], 0x00FFFFFF)
	binary.LittleEndian.PutUint32(data[4:8], 0x00000005) // 0101

	stream := NewByteStreamInArray(data)

	// Read 30 bits: should get 30 LSBs of first word (= 0x3FFFFFFF & first_word, which is 0x00FFFFFF)
	val, err := stream.GetBits(30)
	if err != nil {
		t.Fatalf("GetBits(30) error: %v", err)
	}
	if val != 0x00FFFFFF&((1<<30)-1) {
		t.Fatalf("GetBits(30) = %#08x", val)
	}
	// val should be 0x00FFFFFF
	if val != 0x00FFFFFF {
		t.Fatalf("GetBits(30) = %#08x, want 0x00FFFFFF", val)
	}

	// Now 2 bits remain from first word (2 MSB zeros = 0) + 5 (0101) from second word
	// After crossing boundary: buffer has 2 bits of 0, then reads 32 bits (0x05),
	// resulting in 34 bits. We want 32 bits total minimum to satisfy another read...
	// Let's just read 4 bits from remaining.
	val, err = stream.GetBits(4)
	if err != nil {
		t.Fatalf("GetBits(4) error: %v", err)
	}
	// After GetBits(30): buffer has 2 remaining MSB zeros from first word.
	// Then reads second word 0x05, shifted left by 2: 0x14, num=34.
	// GetBits(4) extracts lower 4 bits of 0x14 = 0x4.
	if val != 0x4 {
		t.Fatalf("GetBits(4) across word boundary = %d, want 4", val)
	}
}

// ---------------------------------------------------------------------------
// ByteStreamInArray tests
// ---------------------------------------------------------------------------

func TestArrayGetByte(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	stream := NewByteStreamInArray(data)

	b, err := stream.GetByte()
	if err != nil || b != 0x01 {
		t.Fatalf("GetByte() = %02x, err=%v, want 01", b, err)
	}
	b, err = stream.GetByte()
	if err != nil || b != 0x02 {
		t.Fatalf("GetByte() #2 = %02x, err=%v, want 02", b, err)
	}
	b, err = stream.GetByte()
	if err != nil || b != 0x03 {
		t.Fatalf("GetByte() #3 = %02x, err=%v, want 03", b, err)
	}
	// Should EOF
	_, err = stream.GetByte()
	if err == nil {
		t.Fatal("GetByte() after EOF should return error")
	}
}

func TestArrayGetBytes(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	stream := NewByteStreamInArray(data)

	buf := make([]byte, 3)
	if err := stream.GetBytes(buf); err != nil {
		t.Fatalf("GetBytes: %v", err)
	}
	if !bytes.Equal(buf, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("GetBytes = %v, want [01 02 03]", buf)
	}

	buf = make([]byte, 2)
	if err := stream.GetBytes(buf); err != nil {
		t.Fatalf("GetBytes #2: %v", err)
	}
	if !bytes.Equal(buf, []byte{0x04, 0x05}) {
		t.Fatalf("GetBytes #2 = %v, want [04 05]", buf)
	}
}

func TestArrayGetBytesEOF(t *testing.T) {
	data := []byte{0x01, 0x02}
	stream := NewByteStreamInArray(data)

	buf := make([]byte, 3)
	err := stream.GetBytes(buf)
	if err == nil {
		t.Fatal("GetBytes with insufficient data should error")
	}
}

func TestArraySeekTell(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	stream := NewByteStreamInArray(data)

	pos, _ := stream.Tell()
	if pos != 0 {
		t.Fatalf("initial Tell() = %d, want 0", pos)
	}

	if err := stream.Seek(2); err != nil {
		t.Fatalf("Seek(2): %v", err)
	}
	pos, _ = stream.Tell()
	if pos != 2 {
		t.Fatalf("Tell() after Seek(2) = %d, want 2", pos)
	}

	b, _ := stream.GetByte()
	if b != 0x03 {
		t.Fatalf("GetByte() at pos 2 = %02x, want 03", b)
	}
}

func TestArraySeekEnd(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	stream := NewByteStreamInArray(data)

	if err := stream.SeekEnd(2); err != nil {
		t.Fatalf("SeekEnd(2): %v", err)
	}
	b, _ := stream.GetByte()
	if b != 0x03 {
		t.Fatalf("SeekEnd(2) read = %02x, want 03", b)
	}
}

func TestArraySkipBytes(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	stream := NewByteStreamInArray(data)

	if err := stream.SkipBytes(2); err != nil {
		t.Fatalf("SkipBytes(2): %v", err)
	}
	b, _ := stream.GetByte()
	if b != 0x03 {
		t.Fatalf("GetByte after skip = %02x, want 03", b)
	}
}

func TestArrayIsSeekable(t *testing.T) {
	stream := NewByteStreamInArray([]byte{0x01})
	if !stream.IsSeekable() {
		t.Fatal("ByteStreamInArray should be seekable")
	}
}

func TestArrayInit(t *testing.T) {
	stream := NewByteStreamInArray([]byte{0x01, 0x02})

	b, _ := stream.GetByte()
	if b != 0x01 {
		t.Fatalf("first byte = %02x, want 01", b)
	}

	// Re-init with new data
	stream.Init([]byte{0xAA, 0xBB, 0xCC})
	b, _ = stream.GetByte()
	if b != 0xAA {
		t.Fatalf("after Init, first byte = %02x, want AA", b)
	}
}

func TestArrayLEBEConsistency(t *testing.T) {
	// Write a known big-endian value: 0x01020304 → big-endian bytes = [01 02 03 04]
	beData := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	stream := NewByteStreamInArray(beData)

	var bufLE [4]byte
	if err := stream.Get32bitsLE(bufLE[:]); err != nil {
		t.Fatalf("Get32bitsLE: %v", err)
	}
	// LE read from bytes [01 02 03 04] → value = 0x04030201
	valLE := binary.LittleEndian.Uint32(bufLE[:])
	if valLE != 0x04030201 {
		t.Fatalf("Get32bitsLE value = %#08x, want 0x04030201", valLE)
	}

	// Reset
	stream = NewByteStreamInArray(beData)

	var bufBE [4]byte
	if err := stream.Get32bitsBE(bufBE[:]); err != nil {
		t.Fatalf("Get32bitsBE: %v", err)
	}
	// BE read from bytes [01 02 03 04]:
	// Output buffer contains LE-ordered bytes [04 03 02 01] representing the big-endian value 0x01020304
	valBE := binary.LittleEndian.Uint32(bufBE[:])
	if valBE != 0x01020304 {
		t.Fatalf("Get32bitsBE value = %#08x, want 0x01020304", valBE)
	}
}

// ---------------------------------------------------------------------------
// ByteStreamInFile tests
// ---------------------------------------------------------------------------

func TestFileGetByte(t *testing.T) {
	tempFile, err := os.CreateTemp("", "laztest")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	tempFile.Write([]byte{0x42, 0x43})
	tempFile.Seek(0, 0)

	stream := NewByteStreamInFile(tempFile)
	b, err := stream.GetByte()
	if err != nil || b != 0x42 {
		t.Fatalf("GetByte() = %02x, err=%v, want 42", b, err)
	}
	b, err = stream.GetByte()
	if err != nil || b != 0x43 {
		t.Fatalf("GetByte() #2 = %02x, err=%v, want 43", b, err)
	}
}

func TestFileSeek(t *testing.T) {
	tempFile, err := os.CreateTemp("", "laztest")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	tempFile.Write([]byte{0x01, 0x02, 0x03, 0x04, 0x05})
	tempFile.Seek(0, 0)

	stream := NewByteStreamInFile(tempFile)

	if err := stream.Seek(3); err != nil {
		t.Fatalf("Seek(3): %v", err)
	}
	b, err := stream.GetByte()
	if err != nil || b != 0x04 {
		t.Fatalf("GetByte() at pos 3 = %02x, err=%v, want 04", b, err)
	}
}

func TestFileSeekEnd(t *testing.T) {
	tempFile, err := os.CreateTemp("", "laztest")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	tempFile.Write([]byte{0x01, 0x02, 0x03, 0x04})
	tempFile.Seek(0, 0)

	stream := NewByteStreamInFile(tempFile)

	if err := stream.SeekEnd(1); err != nil {
		t.Fatalf("SeekEnd(1): %v", err)
	}
	b, err := stream.GetByte()
	if err != nil || b != 0x04 {
		t.Fatalf("SeekEnd(1) read = %02x, err=%v, want 04", b, err)
	}
}

func TestFileSkipBytes(t *testing.T) {
	tempFile, err := os.CreateTemp("", "laztest")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	tempFile.Write([]byte{0x01, 0x02, 0x03, 0x04})
	tempFile.Seek(0, 0)

	stream := NewByteStreamInFile(tempFile)

	if err := stream.SkipBytes(2); err != nil {
		t.Fatalf("SkipBytes(2): %v", err)
	}
	b, err := stream.GetByte()
	if err != nil || b != 0x03 {
		t.Fatalf("GetByte after skip = %02x, err=%v, want 03", b, err)
	}
}

func TestFileIsSeekable(t *testing.T) {
	tempFile, err := os.CreateTemp("", "laztest")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		tempFile.Close()
		os.Remove(tempFile.Name())
	}()

	stream := NewByteStreamInFile(tempFile)
	if !stream.IsSeekable() {
		t.Fatal("ByteStreamInFile should be seekable")
	}
}

// ---------------------------------------------------------------------------
// ByteStreamInReader tests
// ---------------------------------------------------------------------------

func TestReaderGetByte(t *testing.T) {
	r := bytes.NewReader([]byte{0x77, 0x88})
	stream := NewByteStreamInReader(r)

	b, err := stream.GetByte()
	if err != nil || b != 0x77 {
		t.Fatalf("GetByte() = %02x, err=%v, want 77", b, err)
	}
	b, err = stream.GetByte()
	if err != nil || b != 0x88 {
		t.Fatalf("GetByte() #2 = %02x, err=%v, want 88", b, err)
	}
	_, err = stream.GetByte()
	if err == nil {
		t.Fatal("GetByte() after EOF should error")
	}
}

func TestReaderSeekViaReadSeeker(t *testing.T) {
	r := bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04, 0x05})
	stream := NewByteStreamInReader(r)

	if !stream.IsSeekable() {
		t.Fatal("bytes.Reader-backed stream should be seekable")
	}

	if err := stream.Seek(3); err != nil {
		t.Fatalf("Seek(3): %v", err)
	}
	b, err := stream.GetByte()
	if err != nil || b != 0x04 {
		t.Fatalf("GetByte() at pos 3 = %02x, err=%v, want 04", b, err)
	}
}

func TestReaderNotSeekable(t *testing.T) {
	// A plain bytes.Buffer wrapping a byte slice only implements io.Reader
	// (bytes.Buffer itself implements io.ReadWriter but not io.ReadSeeker)
	buf := bytes.NewBuffer([]byte{0x01, 0x02})
	stream := NewByteStreamInReader(buf)

	if stream.IsSeekable() {
		t.Fatal("bytes.Buffer-backed stream should NOT be seekable")
	}

	err := stream.Seek(1)
	if err == nil {
		t.Fatal("Seek on non-seekable stream should error")
	}
}

func TestReaderSkipBytes(t *testing.T) {
	r := bytes.NewReader([]byte{0x01, 0x02, 0x03, 0x04})
	stream := NewByteStreamInReader(r)

	if err := stream.SkipBytes(2); err != nil {
		t.Fatalf("SkipBytes(2): %v", err)
	}
	b, err := stream.GetByte()
	if err != nil || b != 0x03 {
		t.Fatalf("GetByte after skip = %02x, err=%v, want 03", b, err)
	}
}

func TestReaderLEBEConsistency(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04}
	r := bytes.NewReader(data)
	stream := NewByteStreamInReader(r)

	var buf [4]byte
	if err := stream.Get32bitsLE(buf[:]); err != nil {
		t.Fatalf("Get32bitsLE: %v", err)
	}
	if binary.LittleEndian.Uint32(buf[:]) != 0x04030201 {
		t.Fatalf("Get32bitsLE = %#08x, want 0x04030201", binary.LittleEndian.Uint32(buf[:]))
	}

	r = bytes.NewReader(data)
	stream = NewByteStreamInReader(r)
	if err := stream.Get32bitsBE(buf[:]); err != nil {
		t.Fatalf("Get32bitsBE: %v", err)
	}
	if binary.LittleEndian.Uint32(buf[:]) != 0x01020304 {
		t.Fatalf("Get32bitsBE = %#08x, want 0x01020304", binary.LittleEndian.Uint32(buf[:]))
	}
}

// ---------------------------------------------------------------------------
// Verify all implementations satisfy ByteStreamIn interface
// ---------------------------------------------------------------------------

func TestInterfaceSatisfaction(t *testing.T) {
	var _ ByteStreamIn = (*ByteStreamInFile)(nil)
	var _ ByteStreamIn = (*ByteStreamInArray)(nil)
	var _ ByteStreamIn = (*ByteStreamInReader)(nil)
}
