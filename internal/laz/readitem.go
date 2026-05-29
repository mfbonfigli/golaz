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
// readitem.go — point item reader interfaces, ported from src/lasreaditem.hpp.
package laz

// LASreadItem is the interface for all point attribute readers.
// The C++ signature is read(U8* item, U32& context); the context
// pointer supports the scanner channel propagation done by POINT14 readers.
type LASreadItem interface {
	Read(item []byte, context *uint32) error
}

// rawReader abstracts the Init(ByteStreamIn) method shared by all raw
// reader types (base + concrete).  LASreadItemRaw implements it, and
// every concrete raw reader (POINT10_LE, GPSTIME11_LE, …) inherits it
// through embedding, so a []rawReader slice can uniformly init all items.
type rawReader interface {
	Init(ByteStreamIn) error
}

// LASreadItemRaw is the base for raw (uncompressed) item readers.
type LASreadItemRaw struct {
	instream ByteStreamIn
}

// Init binds a byte input stream to this raw reader.
func (r *LASreadItemRaw) Init(instream ByteStreamIn) error {
	if instream == nil {
		return ErrNilStream
	}
	r.instream = instream
	return nil
}

// LASreadItemCompressed is the interface for compressed item readers.
type LASreadItemCompressed interface {
	LASreadItem
	// ChunkSizes reads per-chunk layer byte counts from the stream.
	// Returns nil on success, or an error if the stream read fails.
	// v1/v2 readers always return nil (no layered chunks).
	ChunkSizes() error
	// Init reads the first (raw) point of a chunk to seed the
	// decompression context. The item slice contains the raw point bytes.
	// ctx is the scanner channel context: POINT14 reads the channel from
	// the raw item and writes it back so subsequent items start in the
	// right context. v1/v2 readers ignore ctx entirely.
	Init(item []byte, ctx *uint32) error
}

var ErrNilStream = errNilStream{}

type errNilStream struct{}

func (e errNilStream) Error() string { return "nil stream" }
