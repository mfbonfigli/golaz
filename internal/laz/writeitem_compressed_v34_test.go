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

// writeitem_compressed_v34_test.go — round-trip tests for the v3/v4 layered
// item writers against the Go readers (which are oracle-validated against
// the C++ LASzip implementation, see cpporacle_test.go).
//
// The layered chunk protocol is hand-framed here at the item level,
// mirroring lasreadpoint.go readCore (layered path) and laswritepoint.cpp
// (305-318), except for the parts owned by LASwritePoint / LASreadPoint:
// the raw first-point bytes and the per-chunk point-count u32 are excluded
// (the writers' Init/readers' Init receive the first point directly).
package laz

import (
	"encoding/binary"
	"math"
	"math/rand"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// point generators
// ---------------------------------------------------------------------------

// p14spec describes a POINT14 in field terms; encode() converts it to the
// 40-byte in-memory layout (mirror of readitem_raw.go LASreadItemRawPoint14LE,
// so legacy bytes 14-16 are derived consistently with what the compressed
// reader reconstructs).
type p14spec struct {
	x, y, z    int32
	intensity  uint16
	rn, nr     uint8 // extended 4-bit return number / number of returns
	classFlags uint8 // 4-bit classification flags
	scanDir    uint8
	eofl       uint8
	cls        uint8 // extended classification (0-255)
	ud         uint8
	scanAngle  int16
	psID       uint16
	channel    uint8 // scanner channel 0-3
	gps        float64
}

func (s p14spec) encode() []byte {
	item := make([]byte, 40)
	binary.LittleEndian.PutUint32(item[0:4], uint32(s.x))
	binary.LittleEndian.PutUint32(item[4:8], uint32(s.y))
	binary.LittleEndian.PutUint32(item[8:12], uint32(s.z))
	binary.LittleEndian.PutUint16(item[12:14], s.intensity)

	// Legacy 3-bit return counts (clamped like the raw reader)
	var rn, nr uint8
	if s.nr > 7 {
		nr = 7
		if s.rn > 6 {
			if s.rn >= s.nr {
				rn = 7
			} else {
				rn = 6
			}
		} else {
			rn = s.rn
		}
	} else {
		rn = s.rn
		nr = s.nr
	}
	item[14] = (rn & 0x07) | ((nr & 0x07) << 3) | (s.scanDir << 6) | (s.eofl << 7)

	class := (s.classFlags << 5) & 0xE0
	if s.cls < 32 {
		class |= s.cls
	}
	item[15] = class

	// scan_angle_rank = I8_CLAMP(I16_QUANTIZE(0.006f * scan_angle))
	sa := float32(s.scanAngle) * 0.006
	var saQ int32
	if sa >= 0 {
		saQ = int32(sa + 0.5)
	} else {
		saQ = int32(sa - 0.5)
	}
	item[16] = byte(i32ClampI8(saQ))
	item[17] = s.ud
	binary.LittleEndian.PutUint16(item[18:20], s.psID)
	binary.LittleEndian.PutUint16(item[20:22], uint16(s.scanAngle))
	item[22] = ((s.channel & 0x03) << 2) | ((s.classFlags & 0x0F) << 4)
	item[23] = s.cls
	item[24] = (s.rn & 0x0F) | ((s.nr & 0x0F) << 4)
	// bytes 25-31: padding + deleted_flag stay zero
	binary.LittleEndian.PutUint64(item[32:40], math.Float64bits(s.gps))
	return item
}

// genChannels returns a per-point scanner channel assignment in blocks of
// 3-10 points, cycling through a pattern that hits both switches to unused
// contexts (first visits) and to already-used contexts (revisits).
func genChannels(r *rand.Rand, n int) []uint8 {
	ch := make([]uint8, n)
	seq := []uint8{0, 1, 2, 1, 3, 0, 2, 3, 1, 0}
	i, si := 0, 0
	for i < n {
		cur := seq[si%len(seq)]
		si++
		blk := 3 + r.Intn(8)
		for j := 0; j < blk && i < n; j++ {
			ch[i] = cur
			i++
		}
	}
	return ch
}

// gps sequence generators (closures with state)

func gpsConstFn() func(int) float64 {
	return func(int) float64 { return 123456.789 }
}

func gpsRegularFn() func(int) float64 {
	t := 100000.0
	return func(int) float64 {
		t += 0.00005
		return t
	}
}

// gpsMultiFn produces integer-bit differences that exercise all multiplier
// branches (0, 1, 2-9, 10-499, >=500, -1..-9, <=-10) plus unchanged points.
func gpsMultiFn(r *rand.Rand) func(int) float64 {
	t := 100000.0
	return func(int) float64 {
		k := r.Intn(545) - 25 // -25 .. 519
		t += float64(k) * 0.0000001
		return t
	}
}

// gpsSequencesFn alternates between two far-apart time sequences: the first
// switch takes the 32-bit-overflow (CODE_FULL / new sequence) path, the
// switches back take the "belongs to another sequence" paths.
func gpsSequencesFn() func(int) float64 {
	a, b := 100000.0, 900000000.0
	return func(i int) float64 {
		if (i/7)%2 == 0 {
			a += 0.00001
			return a
		}
		b += 0.002
		return b
	}
}

// gpsJumpsFn produces frequent random full 64-bit jumps (CODE_FULL storm).
func gpsJumpsFn(r *rand.Rand) func(int) float64 {
	t := 100000.0
	return func(int) float64 {
		if r.Intn(3) == 0 {
			t = r.Float64() * 1e9
		} else {
			t += 0.001
		}
		return t
	}
}

// genPoints14 generates n POINT14 items (40 bytes each).
// variant selects the attribute regime:
//
//	"mixed":       random attributes incl. classifications > 31
//	"fullReturns": full 4-bit return number / count values
//	"constAttrs":  constant classification/flags/intensity/scan angle/
//	               user data/point source (for unchanged-layer dropping)
func genPoints14(r *rand.Rand, n int, chFn func(int) uint8, gpsFn func(int) float64, variant string) [][]byte {
	pts := make([][]byte, n)
	x, y, z := int32(100000), int32(2000000), int32(30000)
	for i := range n {
		x += int32(r.Intn(2001) - 1000)
		y += int32(r.Intn(2001) - 1000)
		z += int32(r.Intn(201) - 100)
		s := p14spec{x: x, y: y, z: z, channel: chFn(i), gps: gpsFn(i)}
		switch variant {
		case "constAttrs":
			s.intensity = 1234
			s.rn, s.nr = 1, 1
			s.cls = 2
			s.classFlags = 0
			s.ud = 7
			s.scanAngle = 100
			s.psID = 42
		case "fullReturns":
			// Full 4-bit return counts, including the out-of-spec
			// combination rn > 7 with nr <= 7 — the compressed readers mask
			// the legacy byte-14 fields to 3 bits like the C++ bitfield
			// truncation, so every combination round-trips byte-exactly.
			s.nr = uint8(r.Intn(16))
			s.rn = uint8(r.Intn(16))
			s.intensity = uint16(r.Intn(65536))
			s.cls = uint8(r.Intn(256))
			s.classFlags = uint8(r.Intn(16))
			s.scanDir = uint8(r.Intn(2))
			s.eofl = uint8(r.Intn(2))
			s.ud = uint8(r.Intn(256))
			s.scanAngle = int16(r.Intn(60001) - 30000)
			s.psID = uint16(r.Intn(65536))
		default: // "mixed"
			s.nr = uint8(1 + r.Intn(5))
			s.rn = uint8(1 + r.Intn(int(s.nr)))
			s.intensity = uint16(r.Intn(3000))
			s.cls = uint8(r.Intn(64)) // includes classifications > 31
			s.classFlags = uint8(r.Intn(16))
			s.scanDir = uint8(r.Intn(2))
			s.eofl = uint8(r.Intn(2))
			s.ud = uint8(16 * r.Intn(8))
			s.scanAngle = int16(r.Intn(30001) - 15000)
			s.psID = uint16(1 + r.Intn(4))
		}
		pts[i] = s.encode()
	}
	return pts
}

// genRGB generates n 6-byte RGB items in a per-mode regime.
func genRGB(r *rand.Rand, n int, mode string) [][]byte {
	pts := make([][]byte, n)
	for i := range n {
		item := make([]byte, 6)
		switch mode {
		case "const":
			binary.LittleEndian.PutUint16(item[0:2], 1000)
			binary.LittleEndian.PutUint16(item[2:4], 1000)
			binary.LittleEndian.PutUint16(item[4:6], 1000)
		case "gray":
			v := uint16(r.Intn(65536))
			binary.LittleEndian.PutUint16(item[0:2], v)
			binary.LittleEndian.PutUint16(item[2:4], v)
			binary.LittleEndian.PutUint16(item[4:6], v)
		case "lowbyte": // only the low bytes vary
			binary.LittleEndian.PutUint16(item[0:2], 0x1200|uint16(r.Intn(256)))
			binary.LittleEndian.PutUint16(item[2:4], 0x3400|uint16(r.Intn(256)))
			binary.LittleEndian.PutUint16(item[4:6], 0x5600|uint16(r.Intn(256)))
		default: // "color"
			binary.LittleEndian.PutUint16(item[0:2], uint16(r.Intn(65536)))
			binary.LittleEndian.PutUint16(item[2:4], uint16(r.Intn(65536)))
			binary.LittleEndian.PutUint16(item[4:6], uint16(r.Intn(65536)))
		}
		pts[i] = item
	}
	return pts
}

// genRGBNIR generates n 8-byte RGB+NIR items.
func genRGBNIR(r *rand.Rand, n int, mode string) [][]byte {
	rgb := genRGB(r, n, mode)
	pts := make([][]byte, n)
	for i := range n {
		item := make([]byte, 8)
		copy(item, rgb[i])
		switch mode {
		case "const":
			binary.LittleEndian.PutUint16(item[6:8], 500)
		default:
			binary.LittleEndian.PutUint16(item[6:8], uint16(r.Intn(65536)))
		}
		pts[i] = item
	}
	return pts
}

// genExtraBytes generates n items of nb extra bytes with varied per-byte
// entropy: byte 0 constant (layer must be dropped), byte 1 slowly varying,
// remaining bytes random.
func genExtraBytes(r *rand.Rand, n, nb int) [][]byte {
	pts := make([][]byte, n)
	slow := uint8(100)
	for i := range n {
		item := make([]byte, nb)
		if nb > 0 {
			item[0] = 0x5A
		}
		if nb > 1 {
			if r.Intn(4) == 0 {
				slow += uint8(r.Intn(3)) - 1
			}
			item[1] = slow
		}
		for j := 2; j < nb; j++ {
			item[j] = uint8(r.Intn(256))
		}
		pts[i] = item
	}
	return pts
}

// genWavepackets generates n 29-byte wavepacket items exercising the offset
// cases: same offset, offset += packet size, small (ic-compressed) diffs,
// and random 64-bit jumps.
func genWavepackets(r *rand.Rand, n int) [][]byte {
	pts := make([][]byte, n)
	wp := LASwavepacket13{
		Offset:      60,
		PacketSize:  256,
		ReturnPoint: 1.5,
		X:           0.1,
		Y:           0.2,
		Z:           0.9,
	}
	for i := range n {
		switch r.Intn(5) {
		case 0:
			// same offset
		case 1:
			wp.Offset += uint64(wp.PacketSize)
		case 2:
			wp.Offset += uint64(r.Intn(100000)) // ic-diff case
		case 3:
			wp.Offset = uint64(r.Int63()) // random 64-bit jump
		case 4:
			wp.Offset += uint64(wp.PacketSize)
			wp.PacketSize = uint32(64 + r.Intn(1024))
		}
		if r.Intn(3) == 0 {
			wp.ReturnPoint = float32(r.Intn(100)) * 0.25
			wp.X = float32(r.Intn(100)) * 0.01
			wp.Y = float32(r.Intn(100)) * 0.02
			wp.Z = float32(r.Intn(100)) * 0.03
		}
		item := make([]byte, 29)
		item[0] = byte(r.Intn(4))
		packed := PackLASwavepacket13(&wp)
		copy(item[1:29], packed[:28])
		pts[i] = item
	}
	return pts
}

// zipItems combines parallel per-item slices into the [point][item] shape.
func zipItems(itemSlices ...[][]byte) [][][]byte {
	n := len(itemSlices[0])
	pts := make([][][]byte, n)
	for p := range n {
		pt := make([][]byte, len(itemSlices))
		for j := range itemSlices {
			pt[j] = itemSlices[j][p]
		}
		pts[p] = pt
	}
	return pts
}

// ---------------------------------------------------------------------------
// hand-framed layered chunk write / read helpers
// ---------------------------------------------------------------------------

// writeLayeredChunks compresses points ([point][item] bytes) in chunks of
// chunkSize using the given writers and returns the framed main stream:
// per chunk, the layer sizes (ChunkSizes) followed by the layer payloads
// (ChunkBytes). Mirrors laswritepoint.cpp:305-318 minus the point-count u32
// and the raw first-point bytes (owned by LASwritePoint).
// dummyEnc must be the encoder the writers were constructed with; it is
// bound to the main stream here (in production LASwritePoint does
// enc->init(outstream)).
func writeLayeredChunks(t *testing.T, dummyEnc *ArithmeticEncoder, writers []LASwriteItemCompressed, points [][][]byte, chunkSize int) []byte {
	t.Helper()
	mainOut := NewByteStreamOutArray()
	if err := dummyEnc.Init(mainOut); err != nil {
		t.Fatalf("dummy enc init: %v", err)
	}
	for start := 0; start < len(points); start += chunkSize {
		end := min(start+chunkSize, len(points))
		chunk := points[start:end]
		ctx := uint32(0)
		for i, wr := range writers {
			if err := wr.Init(chunk[0][i], &ctx); err != nil {
				t.Fatalf("writer %d Init (chunk at %d): %v", i, start, err)
			}
		}
		for p, pt := range chunk[1:] {
			ctx = 0
			for i, wr := range writers {
				if err := wr.Write(pt[i], &ctx); err != nil {
					t.Fatalf("writer %d Write point %d: %v", i, start+1+p, err)
				}
			}
		}
		for i, wr := range writers {
			if err := wr.ChunkSizes(); err != nil {
				t.Fatalf("writer %d ChunkSizes (chunk at %d): %v", i, start, err)
			}
		}
		for i, wr := range writers {
			if err := wr.ChunkBytes(); err != nil {
				t.Fatalf("writer %d ChunkBytes (chunk at %d): %v", i, start, err)
			}
		}
	}
	return mainOut.GetData()[:mainOut.GetCurr()]
}

// readLayeredChunks decompresses the hand-framed stream produced by
// writeLayeredChunks, mirroring lasreadpoint.go:573-618 (minus the raw
// first-point read and point-count u32). The first point of each chunk is
// seeded from orig (it travels raw in the real protocol) and copied to the
// output as-is.
// dec must be the decoder the readers were constructed with.
func readLayeredChunks(t *testing.T, dec *ArithmeticDecoder, readers []LASreadItemCompressed, data []byte, orig [][][]byte, chunkSize int) [][][]byte {
	t.Helper()
	mainIn := NewByteStreamInArray(data)
	out := make([][][]byte, len(orig))
	for start := 0; start < len(orig); start += chunkSize {
		end := min(start+chunkSize, len(orig))
		// 'dec' only hands over the stream (doesn't decode)
		if err := dec.Init(mainIn, false); err != nil {
			t.Fatalf("dec init (chunk at %d): %v", start, err)
		}
		for i, rd := range readers {
			if err := rd.ChunkSizes(); err != nil {
				t.Fatalf("reader %d ChunkSizes (chunk at %d): %v", i, start, err)
			}
		}
		ctx := uint32(0)
		first := make([][]byte, len(readers))
		for i, rd := range readers {
			first[i] = append([]byte(nil), orig[start][i]...)
			if err := rd.Init(first[i], &ctx); err != nil {
				t.Fatalf("reader %d Init (chunk at %d): %v", i, start, err)
			}
		}
		out[start] = first
		for p := start + 1; p < end; p++ {
			ctx = 0
			pt := make([][]byte, len(readers))
			for i, rd := range readers {
				pt[i] = make([]byte, len(orig[p][i]))
				if err := rd.Read(pt[i], &ctx); err != nil {
					t.Fatalf("reader %d Read point %d: %v", i, p, err)
				}
			}
			out[p] = pt
		}
	}
	return out
}

// makeV34Writers / makeV34Readers build matched writer/reader stacks.
// itemTypes uses the LASITEM_* constants; number applies to BYTE14.
func makeV34Writers(enc *ArithmeticEncoder, itemTypes []uint16, number uint32, v4 bool) []LASwriteItemCompressed {
	ws := make([]LASwriteItemCompressed, 0, len(itemTypes))
	for _, ty := range itemTypes {
		switch ty {
		case LASITEM_POINT14:
			if v4 {
				ws = append(ws, NewLASwriteItemCompressedPoint14v4(enc))
			} else {
				ws = append(ws, NewLASwriteItemCompressedPoint14v3(enc))
			}
		case LASITEM_RGB14:
			if v4 {
				ws = append(ws, NewLASwriteItemCompressedRGB14v4(enc))
			} else {
				ws = append(ws, NewLASwriteItemCompressedRGB14v3(enc))
			}
		case LASITEM_RGBNIR14:
			if v4 {
				ws = append(ws, NewLASwriteItemCompressedRGBNIR14v4(enc))
			} else {
				ws = append(ws, NewLASwriteItemCompressedRGBNIR14v3(enc))
			}
		case LASITEM_WAVEPACKET14:
			if v4 {
				ws = append(ws, NewLASwriteItemCompressedWavepacket14v4(enc))
			} else {
				ws = append(ws, NewLASwriteItemCompressedWavepacket14v3(enc))
			}
		case LASITEM_BYTE14:
			if v4 {
				ws = append(ws, NewLASwriteItemCompressedByte14v4(enc, number))
			} else {
				ws = append(ws, NewLASwriteItemCompressedByte14v3(enc, number))
			}
		}
	}
	return ws
}

func makeV34Readers(dec *ArithmeticDecoder, itemTypes []uint16, number uint32, v4 bool) []LASreadItemCompressed {
	rs := make([]LASreadItemCompressed, 0, len(itemTypes))
	for _, ty := range itemTypes {
		switch ty {
		case LASITEM_POINT14:
			if v4 {
				rs = append(rs, NewLASreadItemCompressedPoint14v4(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			} else {
				rs = append(rs, NewLASreadItemCompressedPoint14v3(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			}
		case LASITEM_RGB14:
			if v4 {
				rs = append(rs, NewLASreadItemCompressedRGB14v4(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			} else {
				rs = append(rs, NewLASreadItemCompressedRGB14v3(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			}
		case LASITEM_RGBNIR14:
			if v4 {
				rs = append(rs, NewLASreadItemCompressedRGBNIR14v4(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			} else {
				rs = append(rs, NewLASreadItemCompressedRGBNIR14v3(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			}
		case LASITEM_WAVEPACKET14:
			if v4 {
				rs = append(rs, NewLASreadItemCompressedWavepacket14v4(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			} else {
				rs = append(rs, NewLASreadItemCompressedWavepacket14v3(dec, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			}
		case LASITEM_BYTE14:
			if v4 {
				rs = append(rs, NewLASreadItemCompressedByte14v4(dec, number, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			} else {
				rs = append(rs, NewLASreadItemCompressedByte14v3(dec, number, LASZIP_DECOMPRESS_SELECTIVE_ALL))
			}
		}
	}
	return rs
}

// roundTrip writes points through the writers and reads them back with the
// readers, comparing every point byte-for-byte.
func roundTrip(t *testing.T, itemTypes []uint16, number uint32, v4 bool, points [][][]byte, chunkSize int) []byte {
	t.Helper()
	enc := NewArithmeticEncoder()
	writers := makeV34Writers(enc, itemTypes, number, v4)
	data := writeLayeredChunks(t, enc, writers, points, chunkSize)

	dec := NewArithmeticDecoder()
	readers := makeV34Readers(dec, itemTypes, number, v4)
	got := readLayeredChunks(t, dec, readers, data, points, chunkSize)
	comparePoints(t, points, got)
	return data
}

// ---------------------------------------------------------------------------
// 1. POINT14 alone (v3 and v4)
// ---------------------------------------------------------------------------

func TestWritePoint14v34RoundTrip(t *testing.T) {
	type gpsCase struct {
		name string
		fn   func(r *rand.Rand) func(int) float64
	}
	gpsCases := []gpsCase{
		{"gpsConst", func(*rand.Rand) func(int) float64 { return gpsConstFn() }},
		{"gpsRegular", func(*rand.Rand) func(int) float64 { return gpsRegularFn() }},
		{"gpsMulti", func(r *rand.Rand) func(int) float64 { return gpsMultiFn(r) }},
		{"gpsSequences", func(*rand.Rand) func(int) float64 { return gpsSequencesFn() }},
		{"gpsJumps", func(r *rand.Rand) func(int) float64 { return gpsJumpsFn(r) }},
	}
	type chCase struct {
		name string
		fn   func(r *rand.Rand, n int) func(int) uint8
	}
	chCases := []chCase{
		{"singleChannel", func(*rand.Rand, int) func(int) uint8 {
			return func(int) uint8 { return 0 }
		}},
		{"multiChannel", func(r *rand.Rand, n int) func(int) uint8 {
			ch := genChannels(r, n)
			return func(i int) uint8 { return ch[i] }
		}},
	}
	variants := []string{"mixed", "fullReturns"}
	const n = 700
	const chunkSize = 300 // 3 chunks incl. a partial one → exercises re-Init

	for _, v4 := range []bool{false, true} {
		name := "v3"
		if v4 {
			name = "v4"
		}
		t.Run(name, func(t *testing.T) {
			for _, cc := range chCases {
				for _, gc := range gpsCases {
					for _, variant := range variants {
						t.Run(cc.name+"/"+gc.name+"/"+variant, func(t *testing.T) {
							r := rand.New(rand.NewSource(4711))
							pts := genPoints14(r, n, cc.fn(r, n), gc.fn(r), variant)
							points := zipItems(pts)
							roundTrip(t, []uint16{LASITEM_POINT14}, 0, v4, points, chunkSize)
						})
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. POINT14 + RGB14 (v3 and v4), incl. v3 *ctx semantics after a switch
// ---------------------------------------------------------------------------

func TestWritePoint14RGB14v34RoundTrip(t *testing.T) {
	rgbModes := []string{"const", "gray", "color", "lowbyte"}
	const n = 500
	const chunkSize = 200

	for _, v4 := range []bool{false, true} {
		name := "v3"
		if v4 {
			name = "v4"
		}
		t.Run(name, func(t *testing.T) {
			for _, mode := range rgbModes {
				t.Run(mode, func(t *testing.T) {
					r := rand.New(rand.NewSource(999))
					ch := genChannels(r, n)
					// Multichannel with blocks: after every channel switch the
					// following points do NOT switch, so under v3 the RGB writer
					// sees *ctx == 0 for them even when the POINT14 channel is
					// 1-3 (the load-bearing v3 semantics), while v4 sees the
					// real channel — both must round-trip exactly.
					pts14 := genPoints14(r, n, func(i int) uint8 { return ch[i] }, gpsRegularFn(), "mixed")
					rgb := genRGB(r, n, mode)
					points := zipItems(pts14, rgb)
					roundTrip(t, []uint16{LASITEM_POINT14, LASITEM_RGB14}, 0, v4, points, chunkSize)
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. POINT14 + RGBNIR14 + BYTE14 (v3 and v4)
// ---------------------------------------------------------------------------

func TestWritePoint14RGBNIR14Byte14v34RoundTrip(t *testing.T) {
	const n = 600
	const chunkSize = 250
	const numExtra = 5

	for _, v4 := range []bool{false, true} {
		name := "v3"
		if v4 {
			name = "v4"
		}
		t.Run(name, func(t *testing.T) {
			r := rand.New(rand.NewSource(31337))
			ch := genChannels(r, n)
			pts14 := genPoints14(r, n, func(i int) uint8 { return ch[i] }, gpsMultiFn(r), "mixed")
			rgbnir := genRGBNIR(r, n, "color")
			eb := genExtraBytes(r, n, numExtra)
			points := zipItems(pts14, rgbnir, eb)
			roundTrip(t, []uint16{LASITEM_POINT14, LASITEM_RGBNIR14, LASITEM_BYTE14}, numExtra, v4, points, chunkSize)
		})
	}
}

// ---------------------------------------------------------------------------
// 4. POINT14 + WAVEPACKET14 (v3 and v4)
// ---------------------------------------------------------------------------

func TestWritePoint14Wavepacket14v34RoundTrip(t *testing.T) {
	const n = 400
	const chunkSize = 150

	for _, v4 := range []bool{false, true} {
		name := "v3"
		if v4 {
			name = "v4"
		}
		t.Run(name, func(t *testing.T) {
			r := rand.New(rand.NewSource(2718))
			ch := genChannels(r, n)
			pts14 := genPoints14(r, n, func(i int) uint8 { return ch[i] }, gpsRegularFn(), "mixed")
			wps := genWavepackets(r, n)
			points := zipItems(pts14, wps)
			roundTrip(t, []uint16{LASITEM_POINT14, LASITEM_WAVEPACKET14}, 0, v4, points, chunkSize)
		})
	}
}

// ---------------------------------------------------------------------------
// 5. Unchanged-layer dropping
// ---------------------------------------------------------------------------

func TestWritePoint14v3UnchangedLayerDropping(t *testing.T) {
	const n = 200
	r := rand.New(rand.NewSource(7))
	pts := genPoints14(r, n, func(int) uint8 { return 0 }, gpsConstFn(), "constAttrs")
	points := zipItems(pts)

	// Single chunk so the 9 layer sizes are the head of the framed stream.
	data := roundTrip(t, []uint16{LASITEM_POINT14}, 0, false, points, n)

	if len(data) < 36 {
		t.Fatalf("framed stream too short: %d bytes", len(data))
	}
	sizes := make([]uint32, 9)
	for i := range 9 {
		sizes[i] = binary.LittleEndian.Uint32(data[i*4 : i*4+4])
	}
	names := []string{"channel_returns_XY", "Z", "classification", "flags",
		"intensity", "scan_angle", "user_data", "point_source", "gps_time"}
	// channel_returns_XY and Z always have real (nonzero) sizes
	for _, i := range []int{0, 1} {
		if sizes[i] == 0 {
			t.Errorf("layer %s: size 0, want > 0", names[i])
		}
	}
	// all constant layers must be dropped (size 0)
	for _, i := range []int{2, 3, 4, 5, 6, 7, 8} {
		if sizes[i] != 0 {
			t.Errorf("layer %s: size %d, want 0 (constant layer must be dropped)", names[i], sizes[i])
		}
	}
	// total framed size must be exactly 9 size words + the emitted layers
	total := uint32(36)
	for _, s := range sizes {
		total += s
	}
	if uint32(len(data)) != total {
		t.Errorf("framed stream: %d bytes, want %d (sizes header + emitted layers)", len(data), total)
	}
}

// ---------------------------------------------------------------------------
// 6. Cross-check with real C++-generated data
// ---------------------------------------------------------------------------

func TestWriteV34FixtureCrossCheck(t *testing.T) {
	tests := []struct {
		name string
		base string
		v4   bool
	}{
		{"pf7 v3 multichannel", "las14_pf7_v3_multichannel_1000pts", false},
		{"pf8 v4 multichannel", "las14_pf8_v4_multichannel_1000pts", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pts, u := readAll(t, filepath.Join("testdata", "cpporacle", tc.base+".las"))
			defer u.Close()

			items := u.Items()
			offsets := u.Offsets()

			itemTypes := make([]uint16, len(items))
			number := uint32(0)
			for i := range items {
				itemTypes[i] = items[i].Type
				if items[i].Type == LASITEM_BYTE14 {
					number = uint32(items[i].Size)
				}
			}

			points := make([][][]byte, len(pts))
			for p := range pts {
				pt := make([][]byte, len(items))
				for j := range items {
					pt[j] = pts[p].buf[offsets[j]:offsets[j+1]]
				}
				points[p] = pt
			}

			roundTrip(t, itemTypes, number, tc.v4, points, 100)
		})
	}
}
