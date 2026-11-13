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
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// sequentialReadFails reports whether reading the whole file with Scan
// returns an error.
func sequentialReadFails(t *testing.T, data []byte) bool {
	t.Helper()
	r, err := OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()
	var p Point
	for {
		switch err := r.Scan(&p); err {
		case nil:
		case io.EOF:
			return false
		default:
			return true
		}
	}
}

// chunkedReadFails reports whether decoding every chunk returns an error
// anywhere: from Load, from a point, or at the end of a chunk.
func chunkedReadFails(t *testing.T, data []byte) bool {
	t.Helper()
	r, err := OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()
	chunks, err := r.Chunks()
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	d, err := r.NewChunkDecoder()
	if err != nil {
		t.Fatalf("NewChunkDecoder: %v", err)
	}
	var p Point
	for i := range chunks {
		if err := d.Load(i); err != nil {
			return true
		}
		for {
			err := d.Scan(&p)
			if err == io.EOF {
				break
			}
			if err != nil {
				return true
			}
		}
	}
	return false
}

// Sequential reading notices a damaged chunk in two ways: a decode error, or
// the chunk not ending where the chunk table says the next one starts. A
// ChunkDecoder must not be blinder than that: whenever sequential reading
// reports damage, chunk decoding does too. (Before the end-of-chunk check it
// missed about a third of single-byte corruptions in the format 3 fixture.)
func TestChunkDecoder_DetectsDamageSequentialReadingDetects(t *testing.T) {
	for _, name := range []string{
		"las12_pf3_1000pts_with_extrabytes.laz",    // v2 items, colour and extra bytes
		"las12_pf3_1000pts_with_extrabytes_v1.laz", // v1 items
		"las14_pf7_1000pts_with_extrabytes.laz",    // v3 layered
	} {
		t.Run(name, func(t *testing.T) {
			orig, err := os.ReadFile(filepath.Join(tdDir, name))
			if err != nil {
				t.Fatal(err)
			}
			clean, err := OpenReader(bytes.NewReader(orig))
			if err != nil {
				t.Fatal(err)
			}
			dataStart := int(clean.Header().OffsetToPointData)
			clean.Close()
			// Damage chunk bytes only: they run from just after the 8-byte
			// chunk table pointer up to the chunk table it points to.
			first := dataStart + 8
			end := int(binary.LittleEndian.Uint64(orig[dataStart:]))
			if end <= first || end > len(orig) {
				t.Fatalf("unexpected chunk table position %d", end)
			}

			rng := rand.New(rand.NewSource(1))
			data := make([]byte, len(orig))
			const trials = 150
			var seqFails, chunkFails, missed int
			for i := 0; i < trials; i++ {
				copy(data, orig)
				pos := first + rng.Intn(end-first)
				data[pos] ^= byte(1 + rng.Intn(255))
				seq := sequentialReadFails(t, data)
				chk := chunkedReadFails(t, data)
				if seq {
					seqFails++
				}
				if chk {
					chunkFails++
				}
				if seq && !chk {
					missed++
					if missed <= 3 {
						t.Errorf("byte %d damaged: sequential reading fails, chunk decoding reports nothing", pos)
					}
				}
			}
			t.Logf("%d single-byte corruptions: sequential reading notices %d, chunk decoding %d", trials, seqFails, chunkFails)
			if missed > 0 {
				t.Errorf("chunk decoding missed %d corruptions that sequential reading notices", missed)
			}
			if seqFails == 0 {
				t.Error("no corruption was noticed at all: the test proves nothing")
			}
		})
	}
}

// The end-of-chunk error comes after the chunk's points, in place of io.EOF,
// and stays until another chunk is loaded.
func TestChunkDecoder_EndOfChunkErrorContract(t *testing.T) {
	orig, err := os.ReadFile(filepath.Join(tdDir, "las12_pf3_1000pts_with_extrabytes.laz"))
	if err != nil {
		t.Fatal(err)
	}
	clean, err := OpenReader(bytes.NewReader(orig))
	if err != nil {
		t.Fatal(err)
	}
	dataStart := int(clean.Header().OffsetToPointData)
	clean.Close()
	first := dataStart + 8
	end := int(binary.LittleEndian.Uint64(orig[dataStart:]))

	// Find a corruption that decodes every point of its chunk and is only
	// betrayed by where the chunk ends.
	rng := rand.New(rand.NewSource(5))
	data := make([]byte, len(orig))
	for attempt := 0; attempt < 2000; attempt++ {
		copy(data, orig)
		data[first+rng.Intn(end-first)] ^= 0x40

		r, err := OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := r.Chunks()
		if err != nil {
			t.Fatal(err)
		}
		d, err := r.NewChunkDecoder()
		if err != nil {
			t.Fatal(err)
		}
		var p Point
		for i := range chunks {
			if err := d.Load(i); err != nil {
				t.Fatal(err)
			}
			n := 0
			var scanErr error
			for {
				if scanErr = d.Scan(&p); scanErr != nil {
					break
				}
				n++
			}
			if scanErr == io.EOF || n != int(chunks[i].NumPoints) {
				continue // clean chunk, or a decode error part-way: not the case under test
			}
			// All points came out, then an error instead of io.EOF.
			for k := 0; k < 3; k++ {
				if again := d.Scan(&p); again == nil || again == io.EOF || again.Error() != scanErr.Error() {
					t.Fatalf("Scan after the end-of-chunk error: %v, want the same error %q", again, scanErr)
				}
			}
			if d.Remaining() != 0 {
				t.Errorf("Remaining = %d after the last point", d.Remaining())
			}
			// Loading a healthy chunk clears it.
			other := (i + 1) % len(chunks)
			if err := d.Load(other); err != nil {
				t.Fatal(err)
			}
			for {
				err := d.Scan(&p)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("healthy chunk %d after a corrupt one: %v", other, err)
				}
			}
			r.Close()
			return
		}
		r.Close()
	}
	t.Fatal("no corruption found that only shows at the end of a chunk")
}
