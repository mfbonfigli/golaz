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
// VLR — Variable Length Record (LAS 1.0–1.5)
// Wire format: 2 reserved + 16 userID + 2 recID + 2 recLen + 32 desc = 54-byte header
// ---------------------------------------------------------------------------

// VLR is a Variable Length Record as stored in the LAS header section.
type VLR struct {
	UserID      string // up to 16 chars
	RecordID    uint16
	Description string // up to 32 chars
	Data        []byte // raw payload; nil when RecordLengthAfterHeader == 0
}

// IsLASzip reports whether this VLR is the LASzip compression record.
func (v VLR) IsLASzip() bool {
	return v.UserID == "laszip encoded" && v.RecordID == 22204
}

// IsWKTCRS reports whether this VLR contains an OGC WKT coordinate reference string.
func (v VLR) IsWKTCRS() bool {
	return v.UserID == "LASF_Projection" && v.RecordID == 2112
}

// IsGeoTIFF reports whether this VLR contains GeoTIFF key directory entries.
func (v VLR) IsGeoTIFF() bool {
	return v.UserID == "LASF_Projection" && v.RecordID == 34735
}

// IsExtraByteDescriptor reports whether this VLR contains extra byte descriptors.
func (v VLR) IsExtraByteDescriptor() bool {
	return v.UserID == "LASF_Spec" && v.RecordID == 4
}

// IsWellKnown reports whether this VLR is a recognised standard record.
func (v VLR) IsWellKnown() bool {
	return v.IsLASzip() || v.IsWKTCRS() || v.IsGeoTIFF() || v.IsExtraByteDescriptor()
}

// OGCWkt returns the OGC WKT CRS string from this VLR's payload.
// Returns an error if IsWKTCRS() is false or the payload is empty.
func (v VLR) OGCWkt() (string, error) {
	if !v.IsWKTCRS() {
		return "", fmt.Errorf("VLR (userID=%q recID=%d) is not a WKT CRS record", v.UserID, v.RecordID)
	}
	if len(v.Data) == 0 {
		return "", fmt.Errorf("WKT CRS VLR has empty payload")
	}
	return strings.TrimRight(string(v.Data), "\x00"), nil
}

// ExtraByteDescriptors parses the payload as a sequence of ExtraByteDescriptor
// records (each 192 bytes per the LAS 1.4 R15 spec §4.3 (Extra Bytes VLR)).
// Returns an error if IsExtraByteDescriptor() is false or the payload is malformed.
func (v VLR) ExtraByteDescriptors() ([]ExtraByteDescriptor, error) {
	if !v.IsExtraByteDescriptor() {
		return nil, fmt.Errorf("VLR (userID=%q recID=%d) is not an ExtraByteDescriptor record", v.UserID, v.RecordID)
	}
	if len(v.Data)%192 != 0 {
		return nil, fmt.Errorf("ExtraByteDescriptor VLR payload length %d is not a multiple of 192", len(v.Data))
	}
	n := len(v.Data) / 192
	descs := make([]ExtraByteDescriptor, n)
	for i := range descs {
		if err := descs[i].unpack(v.Data[i*192 : (i+1)*192]); err != nil {
			return nil, fmt.Errorf("ExtraByteDescriptor[%d]: %w", i, err)
		}
	}
	return descs, nil
}

// ---------------------------------------------------------------------------
// WKT — OGC Well-Known Text coordinate reference system
// ---------------------------------------------------------------------------

// WKT holds the OGC WKT coordinate reference strings extracted from
// LASF_Projection VLR/EVLR records.
// Both fields are independent: a file may have one, both, or neither.
type WKT struct {
	// CoordinateSystem is the OGC WKT string from recID 2112.
	// This is the standard CRS definition used by LAS 1.4 and modern tools.
	CoordinateSystem string
	// MathTransform is the WKT math-transform string from recID 2111.
	// Present only in some older files; rarely needed.
	MathTransform string
}

// ---------------------------------------------------------------------------
// EVLR — Extended Variable Length Record (LAS 1.4+)
// Wire format: 2 reserved + 16 userID + 2 recID + 8 recLen + 32 desc = 60-byte header
// ---------------------------------------------------------------------------

// EVLR is an Extended Variable Length Record (LAS 1.4+).
// Structurally identical to VLR but with a uint64 payload length,
// allowing payloads larger than 65 535 bytes.
type EVLR struct {
	UserID      string
	RecordID    uint16
	Description string
	Data        []byte
}

// IsWKTCRS reports whether this EVLR contains an OGC WKT CRS string.
func (e EVLR) IsWKTCRS() bool {
	return e.UserID == "LASF_Projection" && e.RecordID == 2112
}

// IsExtraByteDescriptor reports whether this EVLR contains extra byte descriptors.
func (e EVLR) IsExtraByteDescriptor() bool {
	return e.UserID == "LASF_Spec" && e.RecordID == 4
}

// OGCWkt returns the OGC WKT CRS string from this EVLR's payload.
func (e EVLR) OGCWkt() (string, error) {
	if !e.IsWKTCRS() {
		return "", fmt.Errorf("EVLR (userID=%q recID=%d) is not a WKT CRS record", e.UserID, e.RecordID)
	}
	if len(e.Data) == 0 {
		return "", fmt.Errorf("WKT CRS EVLR has empty payload")
	}
	return strings.TrimRight(string(e.Data), "\x00"), nil
}

// ExtraByteDescriptors parses the payload as ExtraByteDescriptor records.
func (e EVLR) ExtraByteDescriptors() ([]ExtraByteDescriptor, error) {
	if !e.IsExtraByteDescriptor() {
		return nil, fmt.Errorf("EVLR (userID=%q recID=%d) is not an ExtraByteDescriptor record", e.UserID, e.RecordID)
	}
	if len(e.Data)%192 != 0 {
		return nil, fmt.Errorf("ExtraByteDescriptor EVLR payload length %d is not a multiple of 192", len(e.Data))
	}
	n := len(e.Data) / 192
	descs := make([]ExtraByteDescriptor, n)
	for i := range descs {
		if err := descs[i].unpack(e.Data[i*192 : (i+1)*192]); err != nil {
			return nil, fmt.Errorf("ExtraByteDescriptor[%d]: %w", i, err)
		}
	}
	return descs, nil
}

// ---------------------------------------------------------------------------
// ExtraByteDescriptor — describes one extra byte field
// (LAS 1.4 R15 spec §4.3, "Extra Bytes")
// ---------------------------------------------------------------------------

// ExtraByteType enumerates the scalar types for extra byte fields.
// Values match the data_type field of LAS 1.4 R15 §4.3 exactly.
type ExtraByteType uint8

// ExtraByteDescriptor describes one extra byte field as defined in LASF_Spec recID 4.
// Each descriptor is 192 bytes on disk.
type ExtraByteDescriptor struct {
	Name        string
	Description string
	DataType    ExtraByteType
	HasNoData   bool
	HasMin      bool
	HasMax      bool
	HasScale    bool
	HasOffset   bool
	NoData      float64
	Min         float64
	Max         float64
	Scale       float64
	Offset      float64
	// ByteOffset is the position of this field within the extra-bytes section
	// of a point record, set by the Reader during Open (not from disk).
	ByteOffset uint16
	// ByteSize is the number of bytes for one value of this type.
	ByteSize uint8
}

// extraByteBaseTypeSizes is the base scalar size for each LASzip attribute type.
// The LASzip formula is:
//
//	base_type = (data_type - 1) % 10        → index into this table
//	dimension = (data_type - 1) / 10 + 1   → array count (1 for scalar types 1-10)
//	byte_size = base_sizes[base_type] * dimension
//
// For data_type == 0 (undocumented): byte_size is stored in the options byte.
var extraByteBaseTypeSizes = [10]uint8{1, 1, 2, 2, 4, 4, 8, 8, 4, 8}

// extraByteSize returns the byte count for a given on-disk data_type value and
// options byte, using the LASzip size formula from lasattributer.hpp.
func extraByteSize(dataType, optionsByte uint8) uint8 {
	if dataType == 0 {
		return optionsByte // size is stored directly in the options byte
	}
	baseType := (int(dataType) - 1) % 10
	dim := (int(dataType)-1)/10 + 1
	return extraByteBaseTypeSizes[baseType] * uint8(dim)
}

// unpack parses a 192-byte on-disk ExtraByteDescriptor record.
// Layout per LAS 1.4 R15 spec §4.3 (Extra Bytes VLR) / LASzip lasattributer.hpp:
//
//	 0:  reserved (2 bytes)
//	 2:  data_type (1 byte)
//	 3:  options (1 byte) — for type ≠ 0: bit flags (nodata/min/max/scale/offset);
//	                        for type == 0: byte count of the raw field
//	 4:  name (32 bytes)
//	36:  unused (4 bytes)
//	40:  no_data (24 bytes)
//	64:  min (24 bytes)
//	88:  max (24 bytes)
//
// 112:  scale (3 × float64)
// 136:  offset (3 × float64)
// 160:  description (32 bytes)
func (d *ExtraByteDescriptor) unpack(b []byte) error {
	if len(b) < 192 {
		return fmt.Errorf("need 192 bytes, got %d", len(b))
	}
	d.DataType = ExtraByteType(b[2])
	optsByte := b[3]
	d.Name = strings.TrimRight(string(b[4:36]), "\x00")
	d.Description = strings.TrimRight(string(b[160:192]), "\x00")
	d.ByteSize = extraByteSize(uint8(d.DataType), optsByte)

	if d.DataType == 0 {
		// options byte holds the raw size, not bit flags — nothing else to parse.
		return nil
	}

	// Parse options bit flags for non-undocumented types.
	d.HasNoData = optsByte&(1<<0) != 0
	d.HasMin = optsByte&(1<<1) != 0
	d.HasMax = optsByte&(1<<2) != 0
	d.HasScale = optsByte&(1<<3) != 0
	d.HasOffset = optsByte&(1<<4) != 0

	// Numeric fields: all stored as 8-byte LE values in their native type;
	// we normalise to float64 for uniform access.
	if d.HasNoData {
		d.NoData = readExtraByteFloat64(b[40:48], d.DataType)
	}
	if d.HasMin {
		d.Min = readExtraByteFloat64(b[64:72], d.DataType)
	}
	if d.HasMax {
		d.Max = readExtraByteFloat64(b[88:96], d.DataType)
	}
	if d.HasScale {
		d.Scale = math.Float64frombits(binary.LittleEndian.Uint64(b[112:120]))
	}
	if d.HasOffset {
		d.Offset = math.Float64frombits(binary.LittleEndian.Uint64(b[136:144]))
	}
	return nil
}

// readExtraByteFloat64 interprets b as an 8-byte "anytype" slot and returns float64.
// Per LAS 1.4 R15 §4.3 (Extra Bytes VLR): "If the selected data_type is less
// than 8 bytes, the no_data, min, and max fields should be upcast into 8-byte
// storage. For any float these 8 bytes would be upcast to a double, for any
// unsigned char, unsigned short, or unsigned long they would be upcast to an
// unsigned long long and for any char, short, or long, they would be upcast
// to a long long." LASzip's lasattributer.hpp implements the same rule.
// For deprecated array types (data_type > 10) the slot holds a single upcast
// value of the base type.
func readExtraByteFloat64(b []byte, t ExtraByteType) float64 {
	// Normalise to base type (handles deprecated array types via modulo).
	if t == 0 {
		return 0
	}
	baseType := (int(t) - 1) % 10
	switch baseType {
	case 0, 2, 4, 6: // uint8/uint16/uint32/uint64 — upcast to uint64 on disk
		return float64(binary.LittleEndian.Uint64(b))
	case 1, 3, 5, 7: // int8/int16/int32/int64 — upcast to int64 on disk
		return float64(int64(binary.LittleEndian.Uint64(b)))
	case 8, 9: // float32/float64 — upcast to float64 on disk
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	return 0
}

// ---------------------------------------------------------------------------
// I/O helpers — read all VLRs and EVLRs from the stream
// ---------------------------------------------------------------------------

// cString converts a fixed-size NUL-padded byte field to a string, truncating
// at the first NUL like C string handling in the reference implementation.
// Junk bytes after an interior NUL must not defeat record identification.
func cString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

// readAllVLRs reads numVLRs VLR records starting immediately after the header
// (stream must be positioned right after the header bytes).
// The declared VLR count is validated against the space available before the
// point data (each VLR needs at least its 54-byte header).
// Returns all VLRs; also returns the LASzip VLR data if found (nil otherwise).
func readAllVLRs(rs io.ReadSeeker, headerSize uint16, numVLRs uint32, offsetToPointData uint32) ([]VLR, []byte, error) {
	if offsetToPointData < uint32(headerSize) {
		return nil, nil, fmt.Errorf("offset to point data %d precedes header end %d", offsetToPointData, headerSize)
	}
	if maxVLRs := (offsetToPointData - uint32(headerSize)) / 54; numVLRs > maxVLRs {
		return nil, nil, fmt.Errorf("header declares %d VLRs but only %d bytes precede the point data (max %d records)",
			numVLRs, offsetToPointData-uint32(headerSize), maxVLRs)
	}
	if _, err := rs.Seek(int64(headerSize), io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("seek to VLR section: %w", err)
	}

	vlrs := make([]VLR, 0, numVLRs)
	var laszipData []byte

	hdr := make([]byte, 54)
	for i := range numVLRs {
		if _, err := io.ReadFull(rs, hdr); err != nil {
			return nil, nil, fmt.Errorf("read VLR[%d] header: %w", i, err)
		}
		recLen := binary.LittleEndian.Uint16(hdr[20:22])
		userID := cString(hdr[2:18])
		recID := binary.LittleEndian.Uint16(hdr[18:20])
		desc := cString(hdr[22:54])

		var data []byte
		if recLen > 0 {
			data = make([]byte, recLen)
			if _, err := io.ReadFull(rs, data); err != nil {
				return nil, nil, fmt.Errorf("read VLR[%d] data: %w", i, err)
			}
		}

		v := VLR{UserID: userID, RecordID: recID, Description: desc, Data: data}
		vlrs = append(vlrs, v)

		if v.IsLASzip() && laszipData == nil {
			laszipData = data
		}
	}
	return vlrs, laszipData, nil
}

// readAllEVLRs reads numEVLRs EVLR records from the given file offset.
// Each record's declared payload length is validated against the bytes
// actually remaining in the file before allocating.
// Returns an error if the stream cannot seek or if a record is malformed.
func readAllEVLRs(rs io.ReadSeeker, evlrOffset uint64, numEVLRs uint32, savePos int64) ([]EVLR, error) {
	fileSize, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("determine file size for EVLR read: %w", err)
	}
	if _, err := rs.Seek(int64(evlrOffset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to EVLR section (offset %d): %w", evlrOffset, err)
	}

	evlrs := make([]EVLR, 0, min(numEVLRs, uint32(min(uint64(fileSize)/60, uint64(1<<20)))))
	hdr := make([]byte, 60)
	pos := int64(evlrOffset)
	for i := range numEVLRs {
		if _, err := io.ReadFull(rs, hdr); err != nil {
			return nil, fmt.Errorf("read EVLR[%d] header: %w", i, err)
		}
		pos += 60
		recLen := binary.LittleEndian.Uint64(hdr[20:28])
		userID := cString(hdr[2:18])
		recID := binary.LittleEndian.Uint16(hdr[18:20])
		desc := cString(hdr[28:60])

		if recLen > uint64(fileSize-pos) {
			return nil, fmt.Errorf("EVLR[%d] declares %d payload bytes but only %d remain in the file", i, recLen, fileSize-pos)
		}
		var data []byte
		if recLen > 0 {
			data = make([]byte, recLen)
			if _, err := io.ReadFull(rs, data); err != nil {
				return nil, fmt.Errorf("read EVLR[%d] data (%d bytes): %w", i, recLen, err)
			}
			pos += int64(recLen)
		}
		evlrs = append(evlrs, EVLR{UserID: userID, RecordID: recID, Description: desc, Data: data})
	}

	// Restore the stream to its previous position.
	if _, err := rs.Seek(savePos, io.SeekStart); err != nil {
		return nil, fmt.Errorf("restore stream position after EVLR read: %w", err)
	}
	return evlrs, nil
}
