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
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
)

// ---------------------------------------------------------------------------
// Header — LAS public header block, normalised across versions 1.0–1.5.
// ---------------------------------------------------------------------------

// Header holds every field of the LAS public header block.
// Version-specific fields that do not exist in all versions are exposed via
// getters returning (value, bool); the bool is false when the field is absent.
type Header struct {
	// ── present in all LAS versions ──────────────────────────────────────

	FileSignature         string // always "LASF"
	FileSourceID          uint16
	GlobalEncoding        uint16
	ProjectIDGUID1        uint32
	ProjectIDGUID2        uint16
	ProjectIDGUID3        uint16
	ProjectIDGUID4        [8]byte
	VersionMajor          uint8
	VersionMinor          uint8
	SystemIdentifier      string // 32 chars, null-padding stripped
	GeneratingSoftware    string // 32 chars, null-padding stripped
	FileCreationDayOfYear uint16
	FileCreationYear      uint16
	HeaderSize            uint16
	OffsetToPointData     uint32
	NumberOfVLRs          uint32
	PointDataFormat       uint8
	PointDataRecordLength uint16

	// NumberOfPoints is always uint64.
	// Populated from the LAS 1.4 extended field when lasMinor >= 4,
	// from the legacy uint32 field otherwise.
	NumberOfPoints uint64

	// PointsByReturn is always [15]uint64.
	// Formats < 1.4 have at most 5 return slots; slots 5..14 are zero.
	PointsByReturn [15]uint64

	ScaleX, ScaleY, ScaleZ    float64
	OffsetX, OffsetY, OffsetZ float64
	MaxX, MinX                float64
	MaxY, MinY                float64
	MaxZ, MinZ                float64

	// IsCompressed is set by the Reader when a LASzip VLR is found.
	IsCompressed bool

	// ── version-conditional private fields ───────────────────────────────
	waveformDataOffset uint64 // LAS 1.3+
	evlrOffset         uint64 // LAS 1.4+
	numberOfEVLRs      uint32 // LAS 1.4+
	hasWaveformData    bool
	hasEVLRFields      bool
}

// WaveformDataOffset returns the byte offset in the file to waveform data packets.
// Present in LAS 1.3 and later (used with point formats 4, 5, 9, 10).
// Returns nil for LAS 1.0–1.2.
func (h *Header) WaveformDataOffset() *uint64 {
	if !h.hasWaveformData {
		return nil
	}
	v := h.waveformDataOffset
	return &v
}

// EVLROffset returns the byte offset in the file to the first EVLR.
// Returns nil for LAS < 1.4.
func (h *Header) EVLROffset() *uint64 {
	if !h.hasEVLRFields {
		return nil
	}
	v := h.evlrOffset
	return &v
}

// EVLRCount returns the number of Extended Variable Length Records.
// Returns nil for LAS < 1.4.
func (h *Header) EVLRCount() *uint32 {
	if !h.hasEVLRFields {
		return nil
	}
	v := h.numberOfEVLRs
	return &v
}

// GPSTimeIsStandard reports whether GPS timestamps in this file use
// GPS standard time (adjusted) rather than GPS week time.
// Determined by GlobalEncoding bit 0.
func (h *Header) GPSTimeIsStandard() bool {
	return h.GlobalEncoding&0x01 != 0
}

// HasWKTCRS reports whether a WKT CRS VLR is present in the file.
// Valid only for LAS 1.4+; always false for older versions.
// Determined by GlobalEncoding bit 2.
func (h *Header) HasWKTCRS() bool {
	return h.hasEVLRFields && (h.GlobalEncoding&0x04 != 0)
}

// ---------------------------------------------------------------------------
// parseHeader reads the LAS public header from rs.
// rs must be positioned at the start of the file (offset 0).
// ---------------------------------------------------------------------------

func parseHeader(rs io.ReadSeeker) (*Header, error) {
	h := &Header{}

	// Step 1: read the minimum bytes needed to get the signature and headerSize.
	// LAS 1.0 smallest header is 227 bytes; headerSize field is at offset 94.
	// We need at least 96 bytes (offset 94 = uint16 headerSize, +2 bytes).
	minBuf := make([]byte, 96)
	if _, err := io.ReadFull(rs, minBuf); err != nil {
		return nil, fmt.Errorf("read header prefix: %w", err)
	}
	if string(minBuf[0:4]) != "LASF" {
		return nil, fmt.Errorf("not a LAS file: bad file signature %q", string(minBuf[0:4]))
	}
	headerSize := binary.LittleEndian.Uint16(minBuf[94:96])
	if headerSize < 96 {
		return nil, fmt.Errorf("invalid header size %d (minimum 96)", headerSize)
	}

	// Step 2: seek back and read exactly headerSize bytes.
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to header start: %w", err)
	}
	buf := make([]byte, headerSize)
	if _, err := io.ReadFull(rs, buf); err != nil {
		return nil, fmt.Errorf("read full header (%d bytes): %w", headerSize, err)
	}

	// Step 3: parse fixed fields present in all versions.
	h.FileSignature = "LASF"
	h.FileSourceID = binary.LittleEndian.Uint16(buf[4:6])
	h.GlobalEncoding = binary.LittleEndian.Uint16(buf[6:8])
	h.ProjectIDGUID1 = binary.LittleEndian.Uint32(buf[8:12])
	h.ProjectIDGUID2 = binary.LittleEndian.Uint16(buf[12:14])
	h.ProjectIDGUID3 = binary.LittleEndian.Uint16(buf[14:16])
	copy(h.ProjectIDGUID4[:], buf[16:24])
	h.VersionMajor = buf[24]
	h.VersionMinor = buf[25]
	h.SystemIdentifier = strings.TrimRight(string(buf[26:58]), "\x00")
	h.GeneratingSoftware = strings.TrimRight(string(buf[58:90]), "\x00")
	h.FileCreationDayOfYear = binary.LittleEndian.Uint16(buf[90:92])
	h.FileCreationYear = binary.LittleEndian.Uint16(buf[92:94])
	h.HeaderSize = headerSize
	h.OffsetToPointData = binary.LittleEndian.Uint32(buf[96:100])
	h.NumberOfVLRs = binary.LittleEndian.Uint32(buf[100:104])
	h.PointDataFormat = buf[104] & 0x3F // strip LAZ compression bit if set
	h.PointDataRecordLength = binary.LittleEndian.Uint16(buf[105:107])

	// Legacy point count (uint32 at offset 107).
	legacyCount := binary.LittleEndian.Uint32(buf[107:111])

	// Legacy points-by-return: 5 × uint32 at offset 111.
	for i := range 5 {
		h.PointsByReturn[i] = uint64(binary.LittleEndian.Uint32(buf[111+i*4 : 115+i*4]))
	}

	// Scale factors and offsets.
	h.ScaleX = math.Float64frombits(binary.LittleEndian.Uint64(buf[131:139]))
	h.ScaleY = math.Float64frombits(binary.LittleEndian.Uint64(buf[139:147]))
	h.ScaleZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[147:155]))
	h.OffsetX = math.Float64frombits(binary.LittleEndian.Uint64(buf[155:163]))
	h.OffsetY = math.Float64frombits(binary.LittleEndian.Uint64(buf[163:171]))
	h.OffsetZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[171:179]))
	h.MaxX = math.Float64frombits(binary.LittleEndian.Uint64(buf[179:187]))
	h.MinX = math.Float64frombits(binary.LittleEndian.Uint64(buf[187:195]))
	h.MaxY = math.Float64frombits(binary.LittleEndian.Uint64(buf[195:203]))
	h.MinY = math.Float64frombits(binary.LittleEndian.Uint64(buf[203:211]))
	h.MaxZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[211:219]))
	h.MinZ = math.Float64frombits(binary.LittleEndian.Uint64(buf[219:227]))

	minor := h.VersionMinor

	// Step 4: LAS 1.3+ — waveform data offset at byte 227.
	if minor >= 3 && len(buf) >= 235 {
		h.waveformDataOffset = binary.LittleEndian.Uint64(buf[227:235])
		h.hasWaveformData = true
	}

	// Step 5: LAS 1.4+ — extended fields.
	if minor >= 4 && len(buf) >= 375 {
		// Extended point count at offset 247 (uint64).
		extCount := binary.LittleEndian.Uint64(buf[247:255])

		// NumberOfPoints: use the extended count when the legacy field is 0
		// (as required by the LAS 1.4 spec when count exceeds 2^32).
		// Also accept a non-compliant file that set both fields.
		if legacyCount == 0 {
			h.NumberOfPoints = extCount
		} else {
			h.NumberOfPoints = uint64(legacyCount)
		}

		// Extended points-by-return: 15 × uint64 at offset 255.
		for i := range 15 {
			off := 255 + i*8
			h.PointsByReturn[i] = binary.LittleEndian.Uint64(buf[off : off+8])
		}

		// EVLR info.
		h.numberOfEVLRs = binary.LittleEndian.Uint32(buf[235:239])
		h.evlrOffset = binary.LittleEndian.Uint64(buf[239:247])
		h.hasEVLRFields = true
	} else {
		h.NumberOfPoints = uint64(legacyCount)
	}

	return h, nil
}
