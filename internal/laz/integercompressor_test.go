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
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

// readU32 reads a little-endian uint32 from the byte slice at the given offset.
func readU32LE(data []byte, offset *int) uint32 {
	v := binary.LittleEndian.Uint32(data[*offset:])
	*offset += 4
	return v
}

// readI32 reads a little-endian int32 from the byte slice at the given offset.
func readI32LE(data []byte, offset *int) int32 {
	return int32(readU32LE(data, offset))
}

// readVec reads a length-prefixed byte vector.
func readVec(data []byte, offset *int) []byte {
	n := int(readU32LE(data, offset))
	v := data[*offset : *offset+n]
	*offset += n
	return v
}

// TestIntegerCompressorRoundtrip decodes C++-encoded test vectors and verifies
// the Go IntegerCompressor produces identical decompressed values.
func TestIntegerCompressorRoundtrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/integer_compressor.bin")
	if err != nil {
		t.Fatalf("cannot read test vectors: %v", err)
	}

	off := 0
	tcNum := 0

	for off < len(raw) {
		if off+4*4 > len(raw) {
			t.Fatalf("test case %d: truncated header at offset %d", tcNum, off)
		}

		bits := readU32LE(raw, &off)
		contexts := readU32LE(raw, &off)
		bitsHigh := readU32LE(raw, &off)
		range_ := readU32LE(raw, &off)
		npoints := int(readU32LE(raw, &off))

		if off+npoints*8 > len(raw) {
			t.Fatalf("test case %d: truncated points at offset %d", tcNum, off)
		}

		// Read predictions and expected reals.
		preds := make([]int32, npoints)
		expected := make([]int32, npoints)
		for i := range npoints {
			preds[i] = readI32LE(raw, &off)
		}
		for i := range npoints {
			expected[i] = readI32LE(raw, &off)
		}

		// Read compressed data.
		compressed := readVec(raw, &off)

		tcNum++

		// Pre-compute range for wrapping expected values.
		// IntegerCompressor.decompress wraps the result into [0, corr_range).
		var corrRange int32
		if range_ != 0 {
			corrRange = int32(range_)
		} else if bits != 0 && bits < 32 {
			corrRange = int32(1 << bits)
		} else {
			corrRange = 0 // no wrapping for 32-bit
		}

		// Wrap a value into [0, corrRange).
		wrapInRange := func(v int32) int32 {
			if corrRange == 0 {
				return v
			}
			for v < 0 {
				v += corrRange
			}
			for v >= corrRange {
				v -= corrRange
			}
			return v
		}

		t.Run(fmt.Sprintf("tc%d_bits%d_ctx%d_bh%d_r%d", tcNum, bits, contexts, bitsHigh, range_), func(t *testing.T) {
			stream := NewByteStreamInArray(compressed)
			dec := NewArithmeticDecoder()
			if err := dec.Init(stream, true); err != nil {
				t.Fatalf("Init decoder: %v", err)
			}

			ic := NewIntegerDecompressor(dec, bits, contexts, bitsHigh, range_)
			ic.InitDecompressor()

			for i := range npoints {
				real, err := ic.Decompress(preds[i], 0)
				if err != nil {
					t.Fatalf("Decompress point %d: %v", i, err)
				}
				// The decompressor wraps real into [0, corr_range), so we must
				// also wrap the expected value for comparison.
				want := wrapInRange(expected[i])
				if real != want {
					t.Errorf("point %d: pred=%d, got=%d, want=%d (raw_input=%d)", i, preds[i], real, want, expected[i])
				}
			}
		})
	}
}
