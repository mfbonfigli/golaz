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

// compat.go — LAS 1.4 compatibility-mode reading, ported from the reader
// path of LASzip's laszip_dll.cpp (laszip_open_reader detection + header
// rewrite, laszip_read_point per-point reconstruction).
//
// Files written by `laszip -compatible` store point formats 6-10 as legacy
// formats 1/3/4/5 with the LAS 1.4-only attributes recoded into extra bytes:
//
//	"LAS 1.4 scan angle"        int16  scan angle remainder
//	"LAS 1.4 extended returns"  uint8  return/count increments (hi/lo nibble)
//	"LAS 1.4 classification"    uint8  classification increment
//	"LAS 1.4 flags and channel" uint8  scanner channel (bits 2-1), overlap (bit 0)
//	"LAS 1.4 NIR band"          uint16 near-infrared channel (pf8/10 only)
//
// A "lascompatible" VLR (record 22204, 156 or 174 byte payload) marks the
// file and carries the extended LAS 1.4 header fields.
package golaz

import (
	"encoding/binary"
)

// compatState carries everything needed to reconstruct native LAS 1.4 points
// from a compatibility-mode file.
type compatState struct {
	// byte offsets of the compatibility attributes within the extra bytes
	startScanAngle  int
	startExtReturns int
	startClass      int
	startFlagsChan  int
	startNIR        int // -1 when the file has no NIR band

	visibleExtra uint32       // extra bytes remaining visible to the user
	format       uint8        // reconstructed point format (6-10)
	present      pointPresent // presence mask for the reconstructed format
}

const compatVLRUserID = "lascompatible"

// compat attribute names as written by laszip_dll.cpp.
var compatAttributeNames = [5]string{
	"LAS 1.4 scan angle",
	"LAS 1.4 extended returns",
	"LAS 1.4 classification",
	"LAS 1.4 flags and channel",
	"LAS 1.4 NIR band",
}

// setupCompatibility detects compatibility-mode files and, when detection
// succeeds, rewrites the header to its native LAS 1.4 form and installs the
// per-point reconstruction state. Mirrors laszip_dll.cpp laszip_open_reader.
// A file that does not qualify is left untouched (no error).
func (r *Reader) setupCompatibility() {
	h := r.header
	if h.VersionMinor >= 4 {
		return
	}
	pf := h.PointDataFormat
	if pf != 1 && pf != 3 && pf != 4 && pf != 5 {
		return
	}

	// Find the compatibility marker VLR (2+2+4+148 bytes, or +18 for the
	// LAS 1.5 variant).
	var compatVLR *VLR
	for i := range r.vlrs {
		v := &r.vlrs[i]
		if v.UserID == compatVLRUserID && v.RecordID == 22204 &&
			(len(v.Data) == 156 || len(v.Data) == 174) {
			compatVLR = v
			break
		}
	}
	if compatVLR == nil {
		return
	}

	// Find the extra-bytes VLR and locate the compatibility attributes.
	// This is independent of the Reader's named-access descriptors (which may
	// have been skipped on a size mismatch): C++ scans the raw attribute list.
	var descs []ExtraByteDescriptor
	for _, v := range r.vlrs {
		if v.IsExtraByteDescriptor() {
			d, err := v.ExtraByteDescriptors()
			if err == nil {
				descs = d
			}
			break
		}
	}
	if descs == nil {
		return
	}
	// attributeStart mirrors LASattributer::get_attribute_start: the byte
	// offset of a named attribute within the extra-bytes section.
	attributeStart := func(name string) int {
		off := 0
		for _, d := range descs {
			if d.Name == name {
				return off
			}
			off += int(d.ByteSize)
		}
		return -1
	}
	cs := &compatState{
		startScanAngle:  attributeStart(compatAttributeNames[0]),
		startExtReturns: attributeStart(compatAttributeNames[1]),
		startClass:      attributeStart(compatAttributeNames[2]),
		startFlagsChan:  attributeStart(compatAttributeNames[3]),
		startNIR:        attributeStart(compatAttributeNames[4]),
	}
	if cs.startScanAngle == -1 || cs.startExtReturns == -1 ||
		cs.startClass == -1 || cs.startFlagsChan == -1 {
		return
	}

	// ── Header rewrite (from the compatibility VLR payload) ──────────────
	// Payload: u16 laszip version, u16 compatible version, u32 unused,
	// u64 waveform start, u64 EVLR start, u32 EVLR count,
	// u64 extended point count, 15 × u64 extended points by return.
	data := compatVLR.Data
	extCount := binary.LittleEndian.Uint64(data[28:36])
	if h.NumberOfPoints == 0 {
		h.NumberOfPoints = extCount
		r.totalPoints = extCount
	}
	for i := range 15 {
		h.PointsByReturn[i] = binary.LittleEndian.Uint64(data[36+i*8 : 44+i*8])
	}
	// The file now presents as LAS 1.4 with (empty) extended sections.
	h.VersionMinor = 4
	h.hasWaveformData = true
	h.waveformDataOffset = 0
	h.hasEVLRFields = true
	h.evlrOffset = 0
	h.numberOfEVLRs = 0
	// Turn on the WKT bit when an OGC WKT VLR is present (like C++).
	for _, v := range r.vlrs {
		if v.IsWKTCRS() {
			h.GlobalEncoding |= 1 << 4
			break
		}
	}

	// Point format and record length mapping (laszip_dll.cpp): the record is
	// 2 bytes larger (4 with NIR) but loses the 5 (7) compatibility bytes.
	hasNIR := cs.startNIR != -1
	compatBytes := uint32(5)
	sizeDelta := 2 - 5
	if hasNIR {
		compatBytes = 7
		sizeDelta = 4 - 7
	}
	switch pf {
	case 1:
		cs.format = 6
	case 3:
		cs.format = 7
		if hasNIR {
			cs.format = 8
		}
	default: // 4, 5
		cs.format = 9
		if hasNIR {
			cs.format = 10
		}
	}
	h.PointDataFormat = cs.format
	h.PointDataRecordLength = uint16(int(h.PointDataRecordLength) + sizeDelta)

	// ── Extra-bytes presentation ──────────────────────────────────────────
	// The compatibility attributes are appended after any user attributes by
	// the writer, so the visible extra bytes are the prefix before them.
	if r.extraByteCount >= compatBytes {
		cs.visibleExtra = r.extraByteCount - compatBytes
	}
	cs.present = pointPresentFor(cs.format, cs.visibleExtra)

	// Hide the compatibility attributes from named access.
	if r.extraByteDescs != nil {
		filtered := r.extraByteDescs[:0:0]
		for _, d := range r.extraByteDescs {
			if !isCompatAttributeName(d.Name) {
				filtered = append(filtered, d)
			}
		}
		r.extraByteDescs = filtered
		r.extraByteIndex = make(map[string]int, len(filtered))
		for i, d := range filtered {
			r.extraByteIndex[d.Name] = i
		}
	}

	r.present = cs.present
	r.compat = cs
}

func isCompatAttributeName(name string) bool {
	for _, n := range compatAttributeNames {
		if name == n {
			return true
		}
	}
	return false
}

// i16QuantizeF32 replicates C++ I16_QUANTIZE on a float32 operand
// (round half away from zero, then truncate to int16).
func i16QuantizeF32(v float32) int16 {
	if v >= 0 {
		return int16(v + 0.5)
	}
	return int16(v - 0.5)
}

// reconstructCompat instills the LAS 1.4 extended attributes into a point
// decoded from its legacy form. Mirrors laszip_read_point (laszip_dll.cpp).
// p has been populated as the on-disk legacy format; p.extraBuf still holds
// the full extra bytes including the compatibility attributes.
func reconstructCompat(p *Point, cs *compatState) {
	eb := p.extraBuf

	scanAngleRem := int16(binary.LittleEndian.Uint16(eb[cs.startScanAngle:]))
	extendedReturns := eb[cs.startExtReturns]
	classification := eb[cs.startClass]
	flagsAndChannel := eb[cs.startFlagsChan]
	if cs.startNIR != -1 {
		p.nir = binary.LittleEndian.Uint16(eb[cs.startNIR:])
	}

	returnNumberIncrement := (extendedReturns >> 4) & 0x0F
	numberOfReturnsIncrement := extendedReturns & 0x0F
	scannerChannel := (flagsAndChannel >> 1) & 0x03
	overlapBit := flagsAndChannel & 0x01

	// extended_scan_angle = remainder + I16_QUANTIZE(scan_angle_rank / 0.006f).
	// populatePoint10 stored the int8 scan_angle_rank as whole degrees.
	rank := int8(p.ScanAngleDegrees)
	extScanAngle := scanAngleRem + i16QuantizeF32(float32(rank)/0.006)
	p.ScanAngleDegrees = float64(extScanAngle) * 0.006

	p.ReturnNumber += returnNumberIncrement
	p.NumberOfReturns += numberOfReturnsIncrement
	p.Classification += classification
	p.scannerChannel = scannerChannel
	// Legacy flags (synthetic/keypoint/withheld) already sit in bits 0-2.
	p.ClassificationFlags = (overlapBit << 3) | (p.ClassificationFlags & 0x07)

	if cs.visibleExtra > 0 {
		p.extraBuf = eb[:cs.visibleExtra]
	} else {
		p.extraBuf = nil
	}
	p.format = cs.format
	p.present = cs.present
}
