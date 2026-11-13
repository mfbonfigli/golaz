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

package golaz_test

import (
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"

	"github.com/mfbonfigli/golaz"
)

// Decode one file on four goroutines: every goroutine owns a ChunkDecoder
// and takes the next chunk that nobody has started yet.
func ExampleReader_NewChunkDecoder() {
	r, err := golaz.Open("internal/laz/testdata/las/las14_pf6_1000pts_with_extrabytes.laz")
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	chunks, err := r.Chunks()
	if err != nil {
		// errors.Is(err, golaz.ErrChunksUnavailable): read with r.Scan instead.
		log.Fatal(err)
	}

	var (
		next    atomic.Int64 // next chunk to decode
		points  atomic.Int64 // points decoded by all goroutines
		maxZ    = make([]float64, len(chunks))
		wg      sync.WaitGroup
		failure atomic.Value
	)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := r.NewChunkDecoder()
			if err != nil {
				failure.Store(err)
				return
			}
			var p golaz.Point
			for {
				i := int(next.Add(1)) - 1
				if i >= len(chunks) {
					return
				}
				if err := d.Load(i); err != nil {
					failure.Store(err)
					return
				}
				for {
					err := d.Scan(&p)
					if err == io.EOF {
						break
					}
					if err != nil {
						failure.Store(err)
						return
					}
					// Results are kept per chunk, so their order does not
					// depend on which goroutine finished first.
					maxZ[i] = max(maxZ[i], p.Z)
					points.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if err := failure.Load(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%d chunks, first one holds points %d to %d\n",
		len(chunks), chunks[0].FirstPoint, chunks[0].FirstPoint+uint64(chunks[0].NumPoints)-1)
	fmt.Printf("%d of %d points decoded\n", points.Load(), r.NumPoints())
	// Output:
	// 10 chunks, first one holds points 0 to 99
	// 1000 of 1000 points decoded
}
