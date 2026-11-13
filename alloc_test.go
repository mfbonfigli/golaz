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
	"os"
	"strings"
	"testing"
)

// TestReader_Scan_AllocationsPerPoint guards the Scan hot path against
// per-point heap allocations. Uncompressed files must not allocate at all;
// compressed files may allocate a little at every chunk boundary (the
// fixtures use 100-point chunks, 500 times smaller than the usual 50 000).
func TestReader_Scan_AllocationsPerPoint(t *testing.T) {
	entries, err := os.ReadDir(tdDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.Contains(name, "1000pts") {
			continue
		}
		compressed := strings.HasSuffix(name, ".laz")
		if !compressed && !strings.HasSuffix(name, ".las") {
			continue
		}
		t.Run(name, func(t *testing.T) {
			r := openReader(t, name)
			defer r.Close()
			n := int(r.NumPoints())
			var p Point
			readAll := func() {
				if err := r.Reset(); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < n; i++ {
					if err := r.Scan(&p); err != nil {
						t.Fatalf("scan %d: %v", i, err)
					}
				}
			}
			readAll() // warm up: lazily created models and buffers
			perPoint := testing.AllocsPerRun(5, readAll) / float64(n)
			limit := 0.0
			if compressed {
				limit = 0.1
			}
			if perPoint > limit {
				t.Errorf("%.3f allocations per point, want at most %.3f", perPoint, limit)
			}
		})
	}
}
