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
// lasunzipper.go — LASunzipper high-level reader, ported from
// src/lasunzipper.hpp/cpp.
package laz

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
)

// LASunzipper provides high-level reading of LAS/LAZ point cloud files.
type LASunzipper struct {
	file      *os.File
	stream    *ByteStreamInFile
	reader    *LASreadPoint
	lz        *LASzip
	count     uint32
	pointSize uint32
	err       string

	// Header fields
	pointType                 uint8
	pointRecordLen            uint16
	headerOffset              uint32
	numPoints                 uint32
	scaleX, scaleY, scaleZ    float64
	offsetX, offsetY, offsetZ float64

	hasGPSTime    bool
	hasRGB        bool
	hasNIR        bool
	hasWavepacket bool
	extraBytes    uint32
	offsets       []uint32

	items []LASitem
}

// OpenLAS opens a .las or .laz file for reading.
// OpenLAS opens a LAS or LAZ file and prepares it for sequential reading.
// All attributes are decompressed.
func OpenLAS(filename string) (*LASunzipper, error) {
	return OpenLASSelective(filename, LASZIP_DECOMPRESS_SELECTIVE_ALL)
}

// OpenLASSelective opens a LAS or LAZ file and prepares it for reading,
// decompressing only the attributes whose bit is set in decompressSelective.
// Attributes that are not requested keep their seed (first-point) value for
// all subsequent points. Only meaningful for LAS 1.4 pf6-10 LAZ files; for
// uncompressed files or LAS 1.2/1.3, all attributes are always read.
// Use the LASZIP_DECOMPRESS_SELECTIVE_* constants to build the mask.
func OpenLASSelective(filename string, decompressSelective uint32) (*LASunzipper, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	u := &LASunzipper{file: f}
	if err := u.parseHeader(); err != nil {
		f.Close()
		return nil, err
	}

	u.stream = NewByteStreamInFile(f)
	u.reader = NewLASreadPoint(decompressSelective)

	if u.lz == nil {
		// Uncompressed: lz was not set by parseHeader (no LASzip VLR)
		u.lz = NewLASzip()
		if err := u.lz.SetupByPointType(u.pointType, u.pointRecordLen, LASZIP_COMPRESSOR_NONE); err != nil {
			f.Close()
			return nil, fmt.Errorf("setup raw config: %w", err)
		}
	} else if len(u.lz.Items) == 0 {
		// LASzip VLR unpacked but empty items (shouldn't happen)
		f.Close()
		return nil, fmt.Errorf("LASzip VLR has no items")
	}

	u.items = make([]LASitem, u.lz.NumItems)
	copy(u.items, u.lz.Items)

	// Compute offsets and total point size
	// NOTE: POINT14 raw reader internally expands 30-byte on-disk format to
	// a 40-byte in-memory layout (LAStempReadPoint10). Therefore the offset
	// and buffer accounting use item.Size, but the actual read buffer for
	// POINT14 items must be at least 40 bytes. We report the expanded size
	// in offsets[] so main() allocates big enough buffers.
	u.offsets = make([]uint32, u.lz.NumItems+1)
	off := uint32(0)
	for i := uint16(0); i < u.lz.NumItems; i++ {
		u.offsets[i] = off
		sz := uint32(u.items[i].Size)
		if u.items[i].Type == LASITEM_POINT14 {
			sz = 40 // raw reader remaps 30-byte disk format to 40-byte in-memory layout
		}
		off += sz
	}
	u.offsets[u.lz.NumItems] = off
	u.pointSize = off

	// Detect capabilities
	for i := uint16(0); i < u.lz.NumItems; i++ {
		switch u.items[i].Type {
		case LASITEM_GPSTIME11:
			u.hasGPSTime = true
		case LASITEM_RGB12, LASITEM_RGB14:
			u.hasRGB = true
		case LASITEM_RGBNIR14:
			u.hasRGB = true
			u.hasNIR = true
		case LASITEM_WAVEPACKET13, LASITEM_WAVEPACKET14:
			u.hasWavepacket = true
		case LASITEM_BYTE, LASITEM_BYTE14:
			u.extraBytes += uint32(u.items[i].Size)
		}
	}

	if err := u.reader.Setup(uint32(u.lz.NumItems), u.items, u.lz); err != nil {
		f.Close()
		return nil, fmt.Errorf("reader setup: %w", err)
	}

	if _, err := f.Seek(int64(u.headerOffset), 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("seek to points: %w", err)
	}
	if err := u.reader.Init(u.stream); err != nil {
		f.Close()
		return nil, fmt.Errorf("reader init: %w", err)
	}

	return u, nil
}

// parseHeader reads the LAS 1.x header.
func (u *LASunzipper) parseHeader() error {
	buf := make([]byte, 375)
	if _, err := u.file.Read(buf); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	if string(buf[0:4]) != "LASF" {
		return fmt.Errorf("not a LAS file (bad signature: %q)", buf[0:4])
	}

	u.pointType = buf[104] & 0x3F
	u.pointRecordLen = binary.LittleEndian.Uint16(buf[105:107])
	u.numPoints = binary.LittleEndian.Uint32(buf[107:111])
	// LAS 1.4 fix: if the legacy field is 0, fall back to the
	// extended uint64 count at offset 247 (LAS 1.4 spec §2.3)
	lasMinor := buf[25]
	if lasMinor >= 4 && u.numPoints == 0 {
		extended := binary.LittleEndian.Uint64(buf[247:255])
		u.numPoints = uint32(extended) // safe for files with ≤4 billion points
	}
	headerSize := binary.LittleEndian.Uint16(buf[94:96])
	u.headerOffset = binary.LittleEndian.Uint32(buf[96:100])

	// Scale and offset
	u.scaleX = math.Float64frombits(binary.LittleEndian.Uint64(buf[131:139]))
	u.scaleY = math.Float64frombits(binary.LittleEndian.Uint64(buf[139:147]))
	u.scaleZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[147:155]))
	u.offsetX = math.Float64frombits(binary.LittleEndian.Uint64(buf[155:163]))
	u.offsetY = math.Float64frombits(binary.LittleEndian.Uint64(buf[163:171]))
	u.offsetZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[171:179]))

	// Parse VLRs to find LASzip
	numVLRs := binary.LittleEndian.Uint32(buf[100:104])
	vlrStart := int64(headerSize)
	offset := vlrStart
	for i := range numVLRs {
		if _, err := u.file.Seek(offset, 0); err != nil {
			return fmt.Errorf("seek to vlr %d: %w", i, err)
		}
		vlrBuf := make([]byte, 54)
		if _, err := u.file.Read(vlrBuf); err != nil {
			return fmt.Errorf("read vlr %d: %w", i, err)
		}
		recordLen := binary.LittleEndian.Uint16(vlrBuf[20:22])
		offset += 54 + int64(recordLen)

		userID := strings.TrimRight(string(vlrBuf[2:18]), "\x00")
		if userID == "laszip encoded" {
			data := make([]byte, recordLen)
			if _, err := u.file.Read(data); err != nil {
				return fmt.Errorf("read laszip vlr data: %w", err)
			}
			u.lz = NewLASzip()
			if err := u.lz.Unpack(data); err != nil {
				return fmt.Errorf("unpack laszip VLR: %w", err)
			}
			break
		}
	}
	return nil
}

// Read decodes one point into per-item buffers (matching C++).
func (u *LASunzipper) Read(point [][]byte) error {
	if u.err != "" {
		return fmt.Errorf("%s", u.err)
	}
	u.count++
	return u.reader.Read(point)
}

// Seek jumps to the given point index.
func (u *LASunzipper) Seek(position uint32) error {
	if err := u.reader.Seek(u.count, position); err != nil {
		return err
	}
	u.count = position
	return nil
}

// Tell returns the current point index.
func (u *LASunzipper) Tell() uint32 { return u.count }

// NumPoints returns the total number of points.
func (u *LASunzipper) NumPoints() uint32 { return u.numPoints }

// PointFormat returns the LAS point format type.
func (u *LASunzipper) PointFormat() uint8 { return u.pointType }

// HasGPS reports whether GPS time is available.
func (u *LASunzipper) HasGPS() bool { return u.hasGPSTime }

// HasRGB reports whether RGB color is available.
func (u *LASunzipper) HasRGB() bool { return u.hasRGB }

// HasNIR reports whether NIR is available.
func (u *LASunzipper) HasNIR() bool { return u.hasNIR }

// Close releases all resources.
func (u *LASunzipper) Close() error {
	u.reader.Done()
	if u.file != nil {
		err := u.file.Close()
		u.file = nil
		return err
	}
	return nil
}

// Scale returns coordinate scale factors and offsets.
func (u *LASunzipper) Scale() (sx, sy, sz, ox, oy, oz float64) {
	return u.scaleX, u.scaleY, u.scaleZ, u.offsetX, u.offsetY, u.offsetZ
}

// Items returns a copy of the LASitem descriptors.
func (u *LASunzipper) Items() []LASitem { return u.items }

// Offsets returns the per-item byte offsets.
func (u *LASunzipper) Offsets() []uint32 { return u.offsets }

// ---------------------------------------------------------------------------
// Point field extractors
// ---------------------------------------------------------------------------

// GetX returns the X coordinate (int32).
func GetX(point []byte) int32 { return int32(binary.LittleEndian.Uint32(point[0:4])) }

// GetY returns the Y coordinate (int32).
func GetY(point []byte) int32 { return int32(binary.LittleEndian.Uint32(point[4:8])) }

// GetZ returns the Z coordinate (int32).
func GetZ(point []byte) int32 { return int32(binary.LittleEndian.Uint32(point[8:12])) }

// GetIntensity returns the intensity (uint16).
func GetIntensity(point []byte) uint16 { return binary.LittleEndian.Uint16(point[12:14]) }

// GetClassification returns the classification code for any LAS point format.
//
// For POINT10 (LAS PF 0–5): byte 15, bits 0–4 hold the classification code.
// For POINT14 (LAS PF 6–10): the expanded 40-byte in-memory layout stores the
// extended classification at byte 23 (the raw LAS 1.4 classification field).
//
// Pass the items slice returned by LASunzipper.Items() so the function can
// determine the on-disk format automatically.
func GetClassification(point []byte, items []LASitem) uint8 {
	if len(items) > 0 && items[0].Type == LASITEM_POINT14 {
		return point[23] // extended classification in POINT14 expanded layout
	}
	return point[15] & 0x1F // bits 0–4 of classification byte for POINT10
}

// GetReturnNumber returns the return number (1-based, 3-bit).
func GetReturnNumber(point []byte) uint8 { return point[14] & 0x07 }

// GetNumberOfReturns returns the number of returns (3-bit).
func GetNumberOfReturns(point []byte) uint8 { return (point[14] >> 3) & 0x07 }

// GetGPS extracts GPS time from the point buffer.
func GetGPS(point []byte, items []LASitem, offsets []uint32) float64 {
	for i := range items {
		if items[i].Type == LASITEM_GPSTIME11 {
			return math.Float64frombits(binary.LittleEndian.Uint64(point[offsets[i] : offsets[i]+8]))
		}
	}
	return 0
}

// GetRGB extracts R,G,B from the point buffer.
func GetRGB(point []byte, items []LASitem, offsets []uint32) (r, g, b uint16) {
	for i := range items {
		switch items[i].Type {
		case LASITEM_RGB12, LASITEM_RGB14, LASITEM_RGBNIR14:
			o := offsets[i]
			return binary.LittleEndian.Uint16(point[o : o+2]),
				binary.LittleEndian.Uint16(point[o+2 : o+4]),
				binary.LittleEndian.Uint16(point[o+4 : o+6])
		}
	}
	return 0, 0, 0
}
