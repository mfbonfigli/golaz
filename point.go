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
	"errors"
	"fmt"
	"math"

	laz "github.com/mfbonfigli/golaz/internal/laz"
)

// ---------------------------------------------------------------------------
// Sentinel errors
// ---------------------------------------------------------------------------

// ErrFieldNotPresent is returned by optional getters when the field does not
// exist for this point format.
var ErrFieldNotPresent = errors.New("field not present for this point format")

// ErrNoExtraByteDescriptor is returned by ExtraByte(name) when no
// ExtraByteDescriptor VLR was found in the file.
var ErrNoExtraByteDescriptor = errors.New("no ExtraByteDescriptor VLR in file")

// ErrUnknownExtraByteField is returned by ExtraByte(name) when the given
// name does not match any descriptor.
var ErrUnknownExtraByteField = errors.New("unknown extra byte field name")

// ---------------------------------------------------------------------------
// pointPresent — compact bitmask of optional field presence
// ---------------------------------------------------------------------------

type pointPresent uint16

const (
	presentGPS      pointPresent = 1 << 0 // pf 1–5, 6–10
	presentColor    pointPresent = 1 << 1 // pf 2, 3, 5, 7, 8, 10
	presentNIR      pointPresent = 1 << 2 // pf 8, 10
	presentWave     pointPresent = 1 << 3 // pf 4, 5, 9, 10
	presentExtended pointPresent = 1 << 4 // pf 6–10 (LAS 1.4 extended fields)
	presentExtraB   pointPresent = 1 << 5 // any format with extra bytes
)

// pointPresentFor returns the presence bitmask for a given point format and
// extra-byte count.
func pointPresentFor(format uint8, extraByteCount uint32) pointPresent {
	var p pointPresent
	switch format {
	case 1:
		p = presentGPS
	case 2:
		p = presentColor
	case 3:
		p = presentGPS | presentColor
	case 4:
		p = presentGPS | presentWave
	case 5:
		p = presentGPS | presentColor | presentWave
	case 6:
		p = presentGPS | presentExtended
	case 7:
		p = presentGPS | presentColor | presentExtended
	case 8:
		p = presentGPS | presentColor | presentNIR | presentExtended
	case 9:
		p = presentGPS | presentWave | presentExtended
	case 10:
		p = presentGPS | presentColor | presentNIR | presentWave | presentExtended
	}
	if extraByteCount > 0 {
		p |= presentExtraB
	}
	return p
}

// ---------------------------------------------------------------------------
// Point struct
// ---------------------------------------------------------------------------

// Point holds one decoded LAS point. Universal fields are plain struct fields.
// Optional fields are accessed via getters returning (value, error); call the
// corresponding Has*() method first to avoid the error branch in hot loops.
//
// A Point obtained via Scan() shares its raw and extra-byte slices with the
// Reader's internal buffer — they are only valid until the next Scan() or
// Next() call. Use Next() if you need to retain the point across calls.
type Point struct {
	// ── universal fields ─────────────────────────────────────────────────

	// X, Y, Z are real-world coordinates with scale and offset applied:
	//   X = float64(RawX)*Header.ScaleX + Header.OffsetX
	X, Y, Z          float64
	RawX, RawY, RawZ int32 // unscaled integers as stored on disk

	Intensity uint16

	// ReturnNumber: 3-bit (0–7) for pf0–5; 4-bit (0–15) for pf6–10.
	ReturnNumber uint8
	// NumberOfReturns: 3-bit (0–7) for pf0–5; 4-bit (0–15) for pf6–10.
	NumberOfReturns uint8

	ScanDirectionFlag bool
	EdgeOfFlightLine  bool

	// Classification is pre-filtered to the correct bit-width:
	//   pf0–5:  classificationByte & 0x1F  (range 0–31)
	//   pf6–10: full classification byte   (range 0–255)
	// No masking is required by the caller.
	Classification uint8

	// ClassificationFlags:
	//   bit0 = synthetic, bit1 = keypoint, bit2 = withheld, bit3 = overlap (pf6–10 only).
	//   pf0–5:  extracted from bits 5–7 of the raw classification byte (bit3 always 0).
	//   pf6–10: from the dedicated 4-bit flags nibble.
	ClassificationFlags uint8

	UserData      uint8
	PointSourceID uint16

	// ScanAngleDegrees is the scan angle normalised to floating-point degrees:
	//   pf0–5:  int8 scan_angle_rank  (already in degrees, range ≈ –90..+90)
	//   pf6–10: int16 * 0.006         (range ≈ –180..+180)
	ScanAngleDegrees float64

	// ── internal ─────────────────────────────────────────────────────────
	format  uint8
	present pointPresent

	gpsTime          float64
	red, green, blue uint16
	nir              uint16
	scannerChannel   uint8

	waveIdx    uint8
	waveOffset uint64
	waveSize   uint32
	waveLoc    float32
	waveXt     float32
	waveYt     float32
	waveZt     float32

	// extraBuf is a sub-slice of the Reader's flat buffer.
	extraBuf []byte

	// raw holds the on-disk bytes (length == PointDataRecordLength).
	// For pf0–5: direct slice of flatBuf (zero-copy).
	// For pf6–10: slice of Reader.onDiskBuf (repacked on each Scan).
	raw []byte
}

// ---------------------------------------------------------------------------
// Presence helpers
// ---------------------------------------------------------------------------

// Format returns the LAS point data format number (0–10).
func (p *Point) Format() uint8 { return p.format }

// HasGPS reports whether this point contains a GPS time value.
// True for formats 1–5, 6–10.
func (p *Point) HasGPS() bool { return p.present&presentGPS != 0 }

// HasColor reports whether this point contains RGB colour values.
// True for formats 2, 3, 5, 7, 8, 10.
func (p *Point) HasColor() bool { return p.present&presentColor != 0 }

// HasNIR reports whether this point contains a Near Infrared value.
// True for formats 8 and 10.
func (p *Point) HasNIR() bool { return p.present&presentNIR != 0 }

// HasWavepacket reports whether this point contains waveform data fields.
// True for formats 4, 5, 9, 10.
func (p *Point) HasWavepacket() bool { return p.present&presentWave != 0 }

// HasExtendedFields reports whether this point uses LAS 1.4 extended fields:
// 4-bit return numbers, scanner channel, and the 4-bit classification flags nibble.
// True for formats 6–10.
func (p *Point) HasExtendedFields() bool { return p.present&presentExtended != 0 }

// HasScannerChannel is an alias for HasExtendedFields for readability.
func (p *Point) HasScannerChannel() bool { return p.HasExtendedFields() }

// HasExtraBytes reports whether this point carries extra byte data.
func (p *Point) HasExtraBytes() bool { return p.present&presentExtraB != 0 }

// ---------------------------------------------------------------------------
// Optional getters
// ---------------------------------------------------------------------------

// GPSTime returns the GPS time for this point.
// Returns ErrFieldNotPresent if HasGPS() is false.
func (p *Point) GPSTime() (float64, error) {
	if !p.HasGPS() {
		return 0, ErrFieldNotPresent
	}
	return p.gpsTime, nil
}

// Red returns the red channel value.
// Returns ErrFieldNotPresent if HasColor() is false.
func (p *Point) Red() (uint16, error) {
	if !p.HasColor() {
		return 0, ErrFieldNotPresent
	}
	return p.red, nil
}

// Green returns the green channel value.
// Returns ErrFieldNotPresent if HasColor() is false.
func (p *Point) Green() (uint16, error) {
	if !p.HasColor() {
		return 0, ErrFieldNotPresent
	}
	return p.green, nil
}

// Blue returns the blue channel value.
// Returns ErrFieldNotPresent if HasColor() is false.
func (p *Point) Blue() (uint16, error) {
	if !p.HasColor() {
		return 0, ErrFieldNotPresent
	}
	return p.blue, nil
}

// NIR returns the Near Infrared value.
// Returns ErrFieldNotPresent if HasNIR() is false.
func (p *Point) NIR() (uint16, error) {
	if !p.HasNIR() {
		return 0, ErrFieldNotPresent
	}
	return p.nir, nil
}

// ScannerChannel returns the scanner channel (0–3).
// Returns ErrFieldNotPresent if HasScannerChannel() is false.
func (p *Point) ScannerChannel() (uint8, error) {
	if !p.HasScannerChannel() {
		return 0, ErrFieldNotPresent
	}
	return p.scannerChannel, nil
}

// WavePacketDescriptorIndex returns the waveform packet descriptor index.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WavePacketDescriptorIndex() (uint8, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveIdx, nil
}

// WaveformDataOffset returns the byte offset to the waveform data packet.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WaveformDataOffset() (uint64, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveOffset, nil
}

// WaveformPacketSize returns the size in bytes of the waveform data packet.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WaveformPacketSize() (uint32, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveSize, nil
}

// ReturnPointWaveformLocation returns the return point waveform location (float32).
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) ReturnPointWaveformLocation() (float32, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveLoc, nil
}

// WaveXt returns the X(t) waveform parameter.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WaveXt() (float32, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveXt, nil
}

// WaveYt returns the Y(t) waveform parameter.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WaveYt() (float32, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveYt, nil
}

// WaveZt returns the Z(t) waveform parameter.
// Returns ErrFieldNotPresent if HasWavepacket() is false.
func (p *Point) WaveZt() (float32, error) {
	if !p.HasWavepacket() {
		return 0, ErrFieldNotPresent
	}
	return p.waveZt, nil
}

// ---------------------------------------------------------------------------
// Extra bytes
// ---------------------------------------------------------------------------

// ExtraBytes returns the raw extra-byte slice for this point.
// Returns nil when HasExtraBytes() is false.
// The slice shares the Reader's internal buffer — do not retain across Scan calls.
func (p *Point) ExtraBytes() []byte {
	if !p.HasExtraBytes() {
		return nil
	}
	return p.extraBuf
}

func (p *Point) extraByte(name string, index map[string]int, descs []ExtraByteDescriptor) (any, error) {
	if index == nil {
		return nil, ErrNoExtraByteDescriptor
	}
	i, ok := index[name]
	if !ok {
		return nil, ErrUnknownExtraByteField
	}
	if !p.HasExtraBytes() || p.extraBuf == nil {
		return nil, ErrFieldNotPresent
	}
	d := descs[i]
	off := int(d.ByteOffset)
	sz := int(d.ByteSize)
	if off+sz > len(p.extraBuf) {
		return nil, fmt.Errorf("extra byte field %q: offset %d+%d out of range (%d bytes available)",
			name, off, sz, len(p.extraBuf))
	}
	return decodeExtraByte(p.extraBuf[off:off+sz], d.DataType), nil
}

func decodeExtraByte(b []byte, t ExtraByteType) any {
	if t == 0 {
		return b // raw bytes for undocumented type
	}
	// Normalise to base type (handles deprecated array types via modulo).
	baseType := (int(t) - 1) % 10
	switch baseType {
	case 0: // uint8
		return b[0]
	case 1: // int8
		return int8(b[0])
	case 2: // uint16
		return binary.LittleEndian.Uint16(b)
	case 3: // int16
		return int16(binary.LittleEndian.Uint16(b))
	case 4: // uint32
		return binary.LittleEndian.Uint32(b)
	case 5: // int32
		return int32(binary.LittleEndian.Uint32(b))
	case 6: // uint64
		return binary.LittleEndian.Uint64(b)
	case 7: // int64
		return int64(binary.LittleEndian.Uint64(b))
	case 8: // float32
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	case 9: // float64
		return math.Float64frombits(binary.LittleEndian.Uint64(b))
	}
	return b
}

// ---------------------------------------------------------------------------
// Raw on-disk bytes
// ---------------------------------------------------------------------------

// Raw returns this point in the on-disk LAS record format, exactly
// Header.PointDataRecordLength bytes long.
//
// For pf0–5: zero-copy; returns a sub-slice of the Reader's flat buffer.
// For pf6–10: returns a sub-slice of the Reader's pre-allocated on-disk buffer,
// which is repacked from the 40-byte expanded in-memory POINT14 layout on each
// Scan/Next call (~20 byte operations).
//
// The returned slice is only valid until the next Scan() or Next() call.
// Copy it with append([]byte(nil), p.Raw()...) if you need to retain it.
func (p *Point) Raw() []byte { return p.raw }

// ---------------------------------------------------------------------------
// repackPoint14 — translate 40-byte in-memory POINT14 to 30-byte on-disk form
// ---------------------------------------------------------------------------

// repackPoint14 writes the 30-byte on-disk LAS 1.4 POINT14 record into dst
// from the 40-byte in-memory (LAStempReadPoint10) layout in src.
// dst must be at least 30 bytes; src must be at least 40 bytes.
//
// In-memory → on-disk field mapping:
//
//	on[0:14]   ← src[0:14]    X, Y, Z, Intensity (identical layout)
//	on[14]     ← src[24]      bits[3:0]=return_number, bits[7:4]=number_of_returns
//	on[15]                    composed from src[22] and src[14]:
//	                            bits[3:0] = classification_flags  (src[22]>>4 & 0x0F)
//	                            bits[5:4] = scanner_channel       (src[22]>>2 & 0x03)
//	                            bit[6]    = scan_direction_flag   (src[14]>>6 & 0x01)
//	                            bit[7]    = edge_of_flight_line   (src[14]>>7 & 0x01)
//	on[16]     ← src[23]      classification (full byte)
//	on[17]     ← src[17]      user_data
//	on[18:20]  ← src[20:22]   scan_angle (int16 LE)
//	on[20:22]  ← src[18:20]   point_source_ID
//	on[22:30]  ← src[32:40]   GPS time (float64 LE)
func repackPoint14(dst, src []byte) {
	copy(dst[0:14], src[0:14])
	dst[14] = src[24]
	classFlags := (src[22] >> 4) & 0x0F
	scannerChan := (src[22] >> 2) & 0x03
	scanDir := (src[14] >> 6) & 0x01
	eofl := (src[14] >> 7) & 0x01
	dst[15] = classFlags | (scannerChan << 4) | (scanDir << 6) | (eofl << 7)
	dst[16] = src[23]
	dst[17] = src[17]
	dst[18] = src[20]
	dst[19] = src[21]
	dst[20] = src[18]
	dst[21] = src[19]
	copy(dst[22:30], src[32:40])
}

// ---------------------------------------------------------------------------
// populatePoint — fill a Point from the flat in-memory buffer
// ---------------------------------------------------------------------------

// populatePoint decodes all fields of p from flatBuf using the per-item offsets.
// scale and offset are from the Header. present is precomputed at Open time.
// onDiskBuf is written for pf6–10 (the repacked 30-byte POINT14 + remaining items).
// For pf0–5 rawSlice is a direct slice of flatBuf; for pf6–10 it is a slice of onDiskBuf.
func populatePoint(
	p *Point,
	format uint8,
	present pointPresent,
	flatBuf []byte,
	onDiskBuf []byte, // pre-allocated, len == PointDataRecordLength
	items []laz.LASitem,
	offsets []uint32, // in-memory offsets, len == len(items)+1
	scaleX, scaleY, scaleZ float64,
	offsetX, offsetY, offsetZ float64,
) {
	p.format = format
	p.present = present

	switch {
	case format <= 5:
		populatePoint10(p, format, present, flatBuf, items, offsets,
			scaleX, scaleY, scaleZ, offsetX, offsetY, offsetZ)
		p.raw = flatBuf[:offsets[len(offsets)-1]]

	default: // pf 6–10
		populatePoint14(p, format, present, flatBuf, items, offsets,
			scaleX, scaleY, scaleZ, offsetX, offsetY, offsetZ)
		// Repack to on-disk layout.
		pt14Mem := flatBuf[offsets[0]:offsets[1]] // first item is always POINT14
		repackPoint14(onDiskBuf[0:30], pt14Mem)
		// Copy remaining items (RGB, NIR, WP, extra bytes) — already on-disk format.
		if len(items) > 1 {
			rest := flatBuf[offsets[1]:offsets[len(offsets)-1]]
			copy(onDiskBuf[30:], rest)
		}
		p.raw = onDiskBuf
	}
}

// ---------------------------------------------------------------------------
// populatePoint10 — pf 0–5 (POINT10 layout)
// ---------------------------------------------------------------------------

func populatePoint10(
	p *Point, format uint8, present pointPresent,
	buf []byte, items []laz.LASitem, offsets []uint32,
	sx, sy, sz, ox, oy, oz float64,
) {
	// Item 0 is always POINT10 (20 bytes at offsets[0]).
	b := buf[offsets[0]:]

	p.RawX = int32(binary.LittleEndian.Uint32(b[0:4]))
	p.RawY = int32(binary.LittleEndian.Uint32(b[4:8]))
	p.RawZ = int32(binary.LittleEndian.Uint32(b[8:12]))
	p.X = float64(p.RawX)*sx + ox
	p.Y = float64(p.RawY)*sy + oy
	p.Z = float64(p.RawZ)*sz + oz
	p.Intensity = binary.LittleEndian.Uint16(b[12:14])
	p.ReturnNumber = b[14] & 0x07
	p.NumberOfReturns = (b[14] >> 3) & 0x07
	p.ScanDirectionFlag = b[14]&(1<<6) != 0
	p.EdgeOfFlightLine = b[14]&(1<<7) != 0
	rawClass := b[15]
	p.Classification = rawClass & 0x1F
	p.ClassificationFlags = (rawClass >> 5) & 0x07 // bits 5–7: synthetic, keypoint, withheld
	p.ScanAngleDegrees = float64(int8(b[16]))
	p.UserData = b[17]
	p.PointSourceID = binary.LittleEndian.Uint16(b[18:20])

	// Walk remaining items.
	for idx := 1; idx < len(items); idx++ {
		itemBuf := buf[offsets[idx]:offsets[idx+1]]
		switch items[idx].Type {
		case laz.LASITEM_GPSTIME11:
			p.gpsTime = math.Float64frombits(binary.LittleEndian.Uint64(itemBuf[0:8]))

		case laz.LASITEM_RGB12:
			p.red = binary.LittleEndian.Uint16(itemBuf[0:2])
			p.green = binary.LittleEndian.Uint16(itemBuf[2:4])
			p.blue = binary.LittleEndian.Uint16(itemBuf[4:6])

		case laz.LASITEM_WAVEPACKET13:
			p.waveIdx = itemBuf[0]
			p.waveOffset = binary.LittleEndian.Uint64(itemBuf[1:9])
			p.waveSize = binary.LittleEndian.Uint32(itemBuf[9:13])
			p.waveLoc = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[13:17]))
			p.waveXt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[17:21]))
			p.waveYt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[21:25]))
			p.waveZt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[25:29]))

		case laz.LASITEM_BYTE:
			p.extraBuf = itemBuf
		}
	}
}

// ---------------------------------------------------------------------------
// populatePoint14 — pf 6–10 (POINT14 40-byte expanded in-memory layout)
// ---------------------------------------------------------------------------

func populatePoint14(
	p *Point, format uint8, present pointPresent,
	buf []byte, items []laz.LASitem, offsets []uint32,
	sx, sy, sz, ox, oy, oz float64,
) {
	// Item 0 is always POINT14 (40 bytes in-memory at offsets[0]).
	b := buf[offsets[0]:]

	p.RawX = int32(binary.LittleEndian.Uint32(b[0:4]))
	p.RawY = int32(binary.LittleEndian.Uint32(b[4:8]))
	p.RawZ = int32(binary.LittleEndian.Uint32(b[8:12]))
	p.X = float64(p.RawX)*sx + ox
	p.Y = float64(p.RawY)*sy + oy
	p.Z = float64(p.RawZ)*sz + oz
	p.Intensity = binary.LittleEndian.Uint16(b[12:14])

	// Extended return number and number-of-returns from byte 24.
	p.ReturnNumber = b[24] & 0x0F
	p.NumberOfReturns = (b[24] >> 4) & 0x0F

	// Scan direction and edge-of-flight from byte 14 (legacy flags byte).
	p.ScanDirectionFlag = b[14]&(1<<6) != 0
	p.EdgeOfFlightLine = b[14]&(1<<7) != 0

	// Classification from byte 23 (full 8-bit).
	p.Classification = b[23]

	// ClassificationFlags: bits 7–4 of byte 22; scanner_channel: bits 3–2.
	p.ClassificationFlags = (b[22] >> 4) & 0x0F
	p.scannerChannel = (b[22] >> 2) & 0x03

	p.UserData = b[17]
	p.PointSourceID = binary.LittleEndian.Uint16(b[18:20])

	// Scan angle: int16 at bytes 20–21; normalise to degrees (* 0.006).
	p.ScanAngleDegrees = float64(int16(binary.LittleEndian.Uint16(b[20:22]))) * 0.006

	// GPS time at bytes 32–40.
	p.gpsTime = math.Float64frombits(binary.LittleEndian.Uint64(b[32:40]))

	// Walk remaining items.
	for idx := 1; idx < len(items); idx++ {
		itemBuf := buf[offsets[idx]:offsets[idx+1]]
		switch items[idx].Type {
		case laz.LASITEM_RGB14:
			p.red = binary.LittleEndian.Uint16(itemBuf[0:2])
			p.green = binary.LittleEndian.Uint16(itemBuf[2:4])
			p.blue = binary.LittleEndian.Uint16(itemBuf[4:6])

		case laz.LASITEM_RGBNIR14:
			p.red = binary.LittleEndian.Uint16(itemBuf[0:2])
			p.green = binary.LittleEndian.Uint16(itemBuf[2:4])
			p.blue = binary.LittleEndian.Uint16(itemBuf[4:6])
			p.nir = binary.LittleEndian.Uint16(itemBuf[6:8])

		case laz.LASITEM_WAVEPACKET14:
			p.waveIdx = itemBuf[0]
			p.waveOffset = binary.LittleEndian.Uint64(itemBuf[1:9])
			p.waveSize = binary.LittleEndian.Uint32(itemBuf[9:13])
			p.waveLoc = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[13:17]))
			p.waveXt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[17:21]))
			p.waveYt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[21:25]))
			p.waveZt = math.Float32frombits(binary.LittleEndian.Uint32(itemBuf[25:29]))

		case laz.LASITEM_BYTE14:
			p.extraBuf = itemBuf
		}
	}
}
