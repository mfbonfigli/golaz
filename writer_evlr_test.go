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

// writer_evlr_test.go — EVLR writing tests: round-trips through the Reader
// and validation of the LAS 1.4 / seekable-output constraints.
package golaz

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// bigPayload builds a payload larger than the 65535-byte VLR limit — the
// whole reason EVLRs exist.
func bigPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestWriterEVLRRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		compressed bool
	}{
		{"uncompressed", false},
		{"compressed", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ext := ".las"
			if tc.compressed {
				ext = ".laz"
			}
			path := filepath.Join(t.TempDir(), "evlr"+ext)

			declared := []EVLR{
				{UserID: "LASF_Projection", RecordID: 2112, Description: "wkt crs", Data: []byte("PROJCS[\"test\"]\x00")},
				{UserID: "custom", RecordID: 42, Description: "big blob", Data: bigPayload(200_000)},
			}
			added := EVLR{UserID: "copc", RecordID: 1000, Description: "added later", Data: []byte{1, 2, 3, 4}}

			w, err := Create(path, WriterHeader{
				VersionMinor: 4,
				PointFormat:  6,
				EVLRs:        declared[:1],
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := w.AddEVLR(declared[1]); err != nil {
				t.Fatalf("AddEVLR: %v", err)
			}

			p := NewPoint(6)
			for i := range 10 {
				w.SetCoordinates(p, float64(i), float64(i)*2, float64(i)*3)
				if err := w.WritePoint(p); err != nil {
					t.Fatalf("WritePoint %d: %v", i, err)
				}
			}
			// EVLRs can be added any time before Close (the COPC hierarchy
			// is only known after the points are written).
			if err := w.AddEVLR(added); err != nil {
				t.Fatalf("AddEVLR after points: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			r, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer r.Close()

			if got := r.Header().NumberOfPoints; got != 10 {
				t.Errorf("NumberOfPoints = %d, want 10", got)
			}
			cnt := r.Header().EVLRCount()
			if cnt == nil || *cnt != 3 {
				t.Fatalf("EVLRCount = %v, want 3", cnt)
			}
			if off := r.Header().EVLROffset(); off == nil || *off == 0 {
				t.Fatalf("EVLROffset = %v, want nonzero", off)
			}

			evlrs, err := r.EVLRs()
			if err != nil {
				t.Fatalf("EVLRs: %v", err)
			}
			want := append(append([]EVLR{}, declared...), added)
			if len(evlrs) != len(want) {
				t.Fatalf("got %d EVLRs, want %d", len(evlrs), len(want))
			}
			for i, e := range evlrs {
				if e.UserID != want[i].UserID || e.RecordID != want[i].RecordID {
					t.Errorf("EVLR[%d] identity = %q/%d, want %q/%d", i, e.UserID, e.RecordID, want[i].UserID, want[i].RecordID)
				}
				if e.Description != want[i].Description {
					t.Errorf("EVLR[%d] description = %q, want %q", i, e.Description, want[i].Description)
				}
				if !bytes.Equal(e.Data, want[i].Data) {
					t.Errorf("EVLR[%d] payload mismatch (%d vs %d bytes)", i, len(e.Data), len(want[i].Data))
				}
			}

			// Point data must be unaffected by the trailing EVLR section.
			var pt Point
			for i := 0; i < 10; i++ {
				if err := r.Scan(&pt); err != nil {
					t.Fatalf("Scan %d: %v", i, err)
				}
			}
		})
	}
}

func TestWriterEVLRValidation(t *testing.T) {
	dir := t.TempDir()
	evlr := EVLR{UserID: "custom", RecordID: 1, Data: []byte{1}}

	t.Run("EVLRs require LAS 1.4", func(t *testing.T) {
		_, err := Create(filepath.Join(dir, "a.las"), WriterHeader{
			VersionMinor: 2,
			PointFormat:  0,
			EVLRs:        []EVLR{evlr},
		})
		if err == nil || !strings.Contains(err.Error(), "1.4") {
			t.Fatalf("Create = %v, want LAS 1.4 requirement error", err)
		}
	})

	t.Run("AddEVLR requires LAS 1.4", func(t *testing.T) {
		w, err := Create(filepath.Join(dir, "b.las"), WriterHeader{PointFormat: 0})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		defer w.Close()
		if err := w.AddEVLR(evlr); err == nil {
			t.Fatal("AddEVLR on a LAS 1.2 writer succeeded, want error")
		}
	})

	t.Run("EVLRs require seekable output", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := NewWriter(&buf, WriterHeader{
			VersionMinor:   4,
			PointFormat:    6,
			NumberOfPoints: 1,
			EVLRs:          []EVLR{evlr},
		})
		if err == nil {
			t.Fatal("NewWriter with EVLRs on non-seekable output succeeded, want error")
		}
	})

	t.Run("AddEVLR after Close", func(t *testing.T) {
		w, err := Create(filepath.Join(dir, "c.laz"), WriterHeader{VersionMinor: 4, PointFormat: 6})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := w.AddEVLR(evlr); err == nil {
			t.Fatal("AddEVLR after Close succeeded, want error")
		}
	})

	t.Run("oversized user id", func(t *testing.T) {
		bad := EVLR{UserID: "way-too-long-user-id!", RecordID: 1}
		_, err := Create(filepath.Join(dir, "d.laz"), WriterHeader{
			VersionMinor: 4, PointFormat: 6, EVLRs: []EVLR{bad},
		})
		if err == nil {
			t.Fatal("Create with 21-byte EVLR UserID succeeded, want error")
		}
	})
}
