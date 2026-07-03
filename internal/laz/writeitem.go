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

// writeitem.go — point item writer interfaces, ported from src/laswriteitem.hpp.
// Mirror images of the reader interfaces in readitem.go.
package laz

// LASwriteItem is the interface for all point attribute writers.
// The C++ signature is write(const U8* item, U32& context); the context
// pointer supports the scanner channel propagation done by POINT14 writers.
type LASwriteItem interface {
	Write(item []byte, context *uint32) error
}

// rawWriter abstracts the Init(ByteStreamOut) method shared by all raw
// writer types, so a []rawWriter slice can uniformly init all items.
type rawWriter interface {
	Init(ByteStreamOut) error
}

// LASwriteItemRaw is the base for raw (uncompressed) item writers.
type LASwriteItemRaw struct {
	outstream ByteStreamOut
}

// Init binds a byte output stream to this raw writer.
func (w *LASwriteItemRaw) Init(outstream ByteStreamOut) error {
	if outstream == nil {
		return ErrNilStream
	}
	w.outstream = outstream
	return nil
}

// LASwriteItemCompressed is the interface for compressed item writers.
type LASwriteItemCompressed interface {
	LASwriteItem
	// Init seeds the compression context from the first (raw) point of a
	// chunk. ctx is the scanner channel context: the POINT14 writer reads
	// the channel from the item and writes it back so subsequent items
	// start in the right context. v1/v2 writers ignore ctx entirely.
	Init(item []byte, ctx *uint32) error
	// ChunkSizes finalizes the per-chunk layer encoders and writes the
	// layer byte counts to the main stream (layered v3/v4 writers only;
	// v1/v2 writers return nil without touching the stream).
	ChunkSizes() error
	// ChunkBytes writes the finished layer payloads to the main stream
	// (layered v3/v4 writers only; v1/v2 writers return nil).
	ChunkBytes() error
}
