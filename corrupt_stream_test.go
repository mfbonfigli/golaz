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

package golaz

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// allOnesStreamFixtures are files whose first compressed stream can be located
// by hand: streamOffset is its distance from the start of the point data.
var allOnesStreamFixtures = []struct {
	name         string
	streamOffset int64
}{
	// 8-byte chunk table pointer, then the chunk: the first point stored
	// raw (34 bytes for format 3), then the arithmetic-coded stream.
	{"las12_pf3_1000pts_with_extrabytes.laz", 8 + 34 + 16}, // +16: the fixture's extra bytes
	// Layered chunk: raw point (30 bytes), point count (4), nine layer
	// sizes (36), then the first layer's arithmetic-coded stream.
	{"las14_pf6_10pts.laz", 8 + 30 + 4 + 36},
}

// A compressed stream that starts with FF FF FF FF puts the arithmetic
// decoder in a state no encoder produces (value == length), from which the
// decoder value runs past every cumulative probability. Reading such a file
// may fail or return garbage points, but it must not panic. golaz up to
// v0.2.2 indexed past the decoder table here.
func TestScan_AllOnesArithmeticStreamDoesNotPanic(t *testing.T) {
	for _, fx := range allOnesStreamFixtures {
		t.Run(fx.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(tdDir, fx.name))
			if err != nil {
				t.Fatal(err)
			}
			clean, err := OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			at := int64(clean.Header().OffsetToPointData) + fx.streamOffset
			clean.Close()
			// The four bytes that seed the decoder value.
			for i := at; i < at+4; i++ {
				data[i] = 0xFF
			}

			r, err := OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("OpenReader: %v", err)
			}
			defer r.Close()
			var p Point
			scanned, failed := 0, 0
			for i := uint64(0); i < 2*r.NumPoints(); i++ { // bounded: errors do not advance Tell
				err := r.Scan(&p)
				if err == io.EOF {
					break
				}
				if err != nil {
					failed++
					continue
				}
				scanned++
			}
			t.Logf("sequential: %d points returned, %d errors", scanned, failed)

			chunks, err := r.Chunks()
			if err != nil {
				return // nothing more to check
			}
			d, err := r.NewChunkDecoder()
			if err != nil {
				t.Fatal(err)
			}
			for i := range chunks {
				if err := d.Load(i); err != nil {
					continue
				}
				for d.Scan(&p) == nil {
				}
			}
		})
	}
}
