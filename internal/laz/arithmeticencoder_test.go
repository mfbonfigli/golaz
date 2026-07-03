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

package laz

import (
	"math"
	"math/rand"
	"testing"
)

// Model alphabet sizes exercised by the round-trip tests. Sizes > 16 make
// the decoder build and use its look-up table (the encoder never does).
// 2048 is the maximum alphabet size allowed by the models (1 << 11).
var rtModelSizes = []uint32{2, 3, 13, 16, 64, 256, 1024, 2048}

const rtNumBitModels = 3

// Encoder operation kinds for the round-trip property tests.
const (
	opEncodeBit = iota
	opEncodeSymbol
	opWriteBit
	opWriteBits
	opWriteByte
	opWriteShort
	opWriteInt
	opWriteInt64
	opWriteFloat
	opWriteDouble
	numOpKinds
)

// rtOp is one recorded encoder operation to replay on the decoder side.
type rtOp struct {
	kind  int
	model int    // index into the symbol/bit model set
	bits  uint32 // bit count for opWriteBits
	val   uint64 // encoded value (float/double stored as IEEE bits)
}

// genOps produces a reproducible random operation sequence.
func genOps(rng *rand.Rand, n int) []rtOp {
	ops := make([]rtOp, n)
	for i := range ops {
		op := rtOp{kind: rng.Intn(numOpKinds)}
		switch op.kind {
		case opEncodeBit:
			op.model = rng.Intn(rtNumBitModels)
			op.val = uint64(rng.Intn(2))
		case opEncodeSymbol:
			op.model = rng.Intn(len(rtModelSizes))
			size := rtModelSizes[op.model]
			// Skew towards the first and last symbols so both the
			// last-symbol and general encodeSymbol branches and the model
			// adaptation get a workout.
			switch rng.Intn(4) {
			case 0:
				op.val = uint64(size - 1)
			case 1:
				op.val = 0
			default:
				op.val = uint64(rng.Intn(int(size)))
			}
		case opWriteBit:
			op.val = uint64(rng.Intn(2))
		case opWriteBits:
			op.bits = uint32(1 + rng.Intn(32))
			mask := uint64(1)<<op.bits - 1
			if rng.Intn(4) == 0 {
				op.val = mask // max value: adversarial for carries
			} else {
				op.val = rng.Uint64() & mask
			}
		case opWriteByte:
			op.val = uint64(rng.Intn(256))
		case opWriteShort:
			op.val = uint64(rng.Intn(65536))
		case opWriteInt:
			op.val = uint64(rng.Uint32())
		case opWriteInt64:
			op.val = rng.Uint64()
		case opWriteFloat:
			// Never NaN (bit patterns compared after the round trip).
			op.val = uint64(math.Float32bits(rng.Float32() * 1e9))
		case opWriteDouble:
			op.val = math.Float64bits(rng.NormFloat64() * 1e12)
		}
		ops[i] = op
	}
	return ops
}

// newRTModels creates one fresh symbol model per entry of rtModelSizes plus
// rtNumBitModels fresh bit models. compress selects encoder or decoder mode;
// the distributions must evolve identically in both.
func newRTModels(compress bool) ([]*ArithmeticModel, []*ArithmeticBitModel) {
	syms := make([]*ArithmeticModel, len(rtModelSizes))
	for i, size := range rtModelSizes {
		syms[i] = NewArithmeticModel(size, compress)
		syms[i].Init(nil)
	}
	bits := make([]*ArithmeticBitModel, rtNumBitModels)
	for i := range bits {
		bits[i] = NewArithmeticBitModel()
	}
	return syms, bits
}

// encodeOps runs the operation sequence through a fresh encoder+models and
// returns the produced bytes.
func encodeOps(t *testing.T, ops []rtOp) []byte {
	t.Helper()
	syms, bits := newRTModels(true)
	out := NewByteStreamOutArray()
	enc := NewArithmeticEncoder()
	if err := enc.Init(out); err != nil {
		t.Fatalf("enc.Init: %v", err)
	}
	for _, op := range ops {
		switch op.kind {
		case opEncodeBit:
			enc.EncodeBit(bits[op.model], uint32(op.val))
		case opEncodeSymbol:
			enc.EncodeSymbol(syms[op.model], uint32(op.val))
		case opWriteBit:
			enc.WriteBit(uint32(op.val))
		case opWriteBits:
			enc.WriteBits(op.bits, uint32(op.val))
		case opWriteByte:
			enc.WriteByte(byte(op.val))
		case opWriteShort:
			enc.WriteShort(uint16(op.val))
		case opWriteInt:
			enc.WriteInt(uint32(op.val))
		case opWriteInt64:
			enc.WriteInt64(op.val)
		case opWriteFloat:
			enc.WriteFloat(math.Float32frombits(uint32(op.val)))
		case opWriteDouble:
			enc.WriteDouble(math.Float64frombits(op.val))
		}
	}
	n, err := enc.Done()
	if err != nil {
		t.Fatalf("enc.Done: %v", err)
	}
	if n != len(out.GetData()) {
		t.Fatalf("Done() reported %d bytes, stream has %d", n, len(out.GetData()))
	}
	return out.GetData()
}

// decodeOps replays the operation sequence on a fresh decoder+models bound
// to the given stream and asserts every decoded value matches.
func decodeOps(t *testing.T, dec *ArithmeticDecoder, ops []rtOp) {
	t.Helper()
	syms, bits := newRTModels(false)
	for i, op := range ops {
		var got uint64
		var err error
		switch op.kind {
		case opEncodeBit:
			var v uint32
			v, err = dec.DecodeBit(bits[op.model])
			got = uint64(v)
		case opEncodeSymbol:
			var v uint32
			v, err = dec.DecodeSymbol(syms[op.model])
			got = uint64(v)
		case opWriteBit:
			var v uint32
			v, err = dec.ReadBit()
			got = uint64(v)
		case opWriteBits:
			var v uint32
			v, err = dec.ReadBits(op.bits)
			got = uint64(v)
		case opWriteByte:
			var v byte
			v, err = dec.ReadByte()
			got = uint64(v)
		case opWriteShort:
			var v uint16
			v, err = dec.ReadShort()
			got = uint64(v)
		case opWriteInt:
			var v uint32
			v, err = dec.ReadInt()
			got = uint64(v)
		case opWriteInt64:
			got, err = dec.ReadInt64()
		case opWriteFloat:
			var v float32
			v, err = dec.ReadFloat()
			got = uint64(math.Float32bits(v))
		case opWriteDouble:
			var v float64
			v, err = dec.ReadDouble()
			got = math.Float64bits(v)
		}
		if err != nil {
			t.Fatalf("op %d (kind %d): decode error: %v", i, op.kind, err)
		}
		if got != op.val {
			t.Fatalf("op %d (kind %d, bits %d): decoded %d, want %d",
				i, op.kind, op.bits, got, op.val)
		}
	}
}

// TestEncoderDecoderRoundTrip encodes random mixed operation sequences and
// verifies the existing decoder reproduces every value. Long sequences cross
// many renormalization/model-update cycles and carry cases.
func TestEncoderDecoderRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		seed int64
		nOps int
	}{
		{"short", 1, 64},
		{"medium", 2, 5000},
		{"long", 3, 150000},
		{"long2", 4, 120000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(tc.seed))
			ops := genOps(rng, tc.nOps)
			data := encodeOps(t, ops)

			dec := NewArithmeticDecoder()
			if err := dec.Init(NewByteStreamInArray(data), true); err != nil {
				t.Fatalf("dec.Init: %v", err)
			}
			decodeOps(t, dec, ops)
		})
	}
}

// TestEncoderRoundTripWriteBitsAllWidths round-trips every WriteBits width
// with min, max and random values.
func TestEncoderRoundTripWriteBitsAllWidths(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var ops []rtOp
	for bits := uint32(1); bits <= 32; bits++ {
		mask := uint64(1)<<bits - 1
		ops = append(ops,
			rtOp{kind: opWriteBits, bits: bits, val: 0},
			rtOp{kind: opWriteBits, bits: bits, val: mask},
			rtOp{kind: opWriteBits, bits: bits, val: rng.Uint64() & mask},
		)
	}
	data := encodeOps(t, ops)
	dec := NewArithmeticDecoder()
	if err := dec.Init(NewByteStreamInArray(data), true); err != nil {
		t.Fatalf("dec.Init: %v", err)
	}
	decodeOps(t, dec, ops)
}

// TestEncoderRoundTripCarryCascade produces long runs of maximum-value
// writes so the buffer fills with 0xFF bytes and carry propagation has to
// walk far back through the produced output.
func TestEncoderRoundTripCarryCascade(t *testing.T) {
	var ops []rtOp
	for range 20000 {
		ops = append(ops,
			rtOp{kind: opWriteBits, bits: 32, val: 0xFFFFFFFF},
			rtOp{kind: opWriteBits, bits: 16, val: 0xFFFF},
			rtOp{kind: opWriteBits, bits: 1, val: 1},
		)
	}
	// A couple of low writes at the end to force final interval adjustment.
	ops = append(ops, rtOp{kind: opWriteBits, bits: 8, val: 0})

	data := encodeOps(t, ops)
	dec := NewArithmeticDecoder()
	if err := dec.Init(NewByteStreamInArray(data), true); err != nil {
		t.Fatalf("dec.Init: %v", err)
	}
	decodeOps(t, dec, ops)
}

// TestEncoderRoundTripMaxProbabilitySymbols drives a 2-symbol model into a
// heavily skewed distribution (near-max probability) which yields long
// 0xFF cascades on the encoder side.
func TestEncoderRoundTripMaxProbabilitySymbols(t *testing.T) {
	ops := make([]rtOp, 0, 100000)
	for i := range 100000 {
		v := uint64(0)
		if i%997 == 0 { // rare "surprise" symbol
			v = 1
		}
		// model 0 has 2 symbols; also drive a bit model the same way.
		ops = append(ops,
			rtOp{kind: opEncodeSymbol, model: 0, val: v},
			rtOp{kind: opEncodeBit, model: 0, val: v},
		)
	}
	data := encodeOps(t, ops)
	dec := NewArithmeticDecoder()
	if err := dec.Init(NewByteStreamInArray(data), true); err != nil {
		t.Fatalf("dec.Init: %v", err)
	}
	decodeOps(t, dec, ops)
}

// TestEncoderChunkBoundaryLifecycle mimics the per-chunk encoder lifecycle:
// encode a segment, Done, Init again on the same outstream, encode another
// segment, Done — then decode both segments sequentially from the single
// byte stream, re-initializing the decoder per segment without any seek.
// This proves the Done() tail bytes keep the decoder byte reads exactly
// aligned with the encoder production at every chunk boundary.
func TestEncoderChunkBoundaryLifecycle(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	segments := [][]rtOp{
		genOps(rng, 3000),
		genOps(rng, 1),
		genOps(rng, 7000),
	}

	out := NewByteStreamOutArray()
	enc := NewArithmeticEncoder()
	segSizes := make([]int, len(segments))
	for i, ops := range segments {
		if err := enc.Init(out); err != nil {
			t.Fatalf("segment %d: enc.Init: %v", i, err)
		}
		syms, bits := newRTModels(true)
		for _, op := range ops {
			switch op.kind {
			case opEncodeBit:
				enc.EncodeBit(bits[op.model], uint32(op.val))
			case opEncodeSymbol:
				enc.EncodeSymbol(syms[op.model], uint32(op.val))
			case opWriteBit:
				enc.WriteBit(uint32(op.val))
			case opWriteBits:
				enc.WriteBits(op.bits, uint32(op.val))
			case opWriteByte:
				enc.WriteByte(byte(op.val))
			case opWriteShort:
				enc.WriteShort(uint16(op.val))
			case opWriteInt:
				enc.WriteInt(uint32(op.val))
			case opWriteInt64:
				enc.WriteInt64(op.val)
			case opWriteFloat:
				enc.WriteFloat(math.Float32frombits(uint32(op.val)))
			case opWriteDouble:
				enc.WriteDouble(math.Float64frombits(op.val))
			}
		}
		n, err := enc.Done()
		if err != nil {
			t.Fatalf("segment %d: enc.Done: %v", i, err)
		}
		segSizes[i] = n
	}

	// Decode all segments sequentially from the concatenated stream.
	in := NewByteStreamInArray(out.GetData())
	dec := NewArithmeticDecoder()
	consumed := int64(0)
	for i, ops := range segments {
		if err := dec.Init(in, true); err != nil {
			t.Fatalf("segment %d: dec.Init: %v", i, err)
		}
		decodeOps(t, dec, ops)
		dec.Done()

		// The decoder must land exactly on the segment boundary.
		pos, err := in.Tell()
		if err != nil {
			t.Fatalf("segment %d: Tell: %v", i, err)
		}
		consumed += int64(segSizes[i])
		if pos != consumed {
			t.Fatalf("segment %d: decoder consumed %d bytes, encoder produced %d",
				i, pos, consumed)
		}
	}
}

// TestEncoderDoneTailBytes checks the exact 2-vs-3 zero tail byte rule of
// Done(): the length > 2*AC__MinLength branch emits one renorm byte plus
// three zeros, the other emits two renorm bytes plus two zeros — in both
// cases exactly four bytes after the last renormalized production.
func TestEncoderDoneTailBytes(t *testing.T) {
	// Fresh encoder, no operations: length == AC__MaxLength > 2*AC__MinLength,
	// so done takes the anotherByte branch: base += AC__MinLength = 0x01000000,
	// renorm emits 0x01, then three zero bytes.
	out := NewByteStreamOutArray()
	enc := NewArithmeticEncoder()
	if err := enc.Init(out); err != nil {
		t.Fatalf("enc.Init: %v", err)
	}
	n, err := enc.Done()
	if err != nil {
		t.Fatalf("enc.Done: %v", err)
	}
	if n != 4 {
		t.Fatalf("Done() wrote %d bytes, want 4", n)
	}
	want := []byte{0x01, 0x00, 0x00, 0x00}
	for i, b := range want {
		if out.GetData()[i] != b {
			t.Fatalf("tail bytes = % x, want % x", out.GetData(), want)
		}
	}

	// Init must be callable again after Done for the next chunk.
	if err := enc.Init(out); err != nil {
		t.Fatalf("enc.Init after Done: %v", err)
	}
	if _, err := enc.Done(); err != nil {
		t.Fatalf("second Done: %v", err)
	}
}

// TestEncoderGetByteStreamOut verifies the accessor used by the v3 writers.
func TestEncoderGetByteStreamOut(t *testing.T) {
	out := NewByteStreamOutArray()
	enc := NewArithmeticEncoder()
	if enc.GetByteStreamOut() != nil {
		t.Error("GetByteStreamOut() before Init should be nil")
	}
	if err := enc.Init(out); err != nil {
		t.Fatalf("enc.Init: %v", err)
	}
	if enc.GetByteStreamOut() != ByteStreamOut(out) {
		t.Error("GetByteStreamOut() should return the bound stream")
	}
	if _, err := enc.Done(); err != nil {
		t.Fatalf("enc.Done: %v", err)
	}
	if enc.GetByteStreamOut() != nil {
		t.Error("GetByteStreamOut() after Done should be nil")
	}
}

// TestEncoderInitNil verifies Init rejects a nil outstream like the C++.
func TestEncoderInitNil(t *testing.T) {
	enc := NewArithmeticEncoder()
	if err := enc.Init(nil); err == nil {
		t.Error("Init(nil) should fail")
	}
	if _, err := enc.Done(); err == nil {
		t.Error("Done() without Init should fail")
	}
}
