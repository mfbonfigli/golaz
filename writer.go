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
	"os"
	"path/filepath"
	"strings"

	laz "github.com/mfbonfigli/golaz/internal/laz"
)

// defaultSystemIdentifier is stamped into the header's SystemIdentifier
// field when the caller leaves it empty.
const defaultSystemIdentifier = "golaz"

// defaultGeneratingSoftware is stamped into the header's GeneratingSoftware
// field when the caller leaves it empty.
const defaultGeneratingSoftware = "golaz"

// laszipVLRDescription is the description written into the LASzip VLR of
// compressed files. Kept stable so produced files are reproducible.
const laszipVLRDescription = "https://laszip.org"

// defaultScale is the coordinate scale factor applied when a WriterHeader
// scale field is left zero.
const defaultScale = 0.001

// ---------------------------------------------------------------------------
// WriterHeader — declarative description of the LAS file to produce
// ---------------------------------------------------------------------------

// WriterHeader configures the LAS file a Writer produces. Zero values get
// sensible defaults, so the minimal configuration is just the PointFormat.
type WriterHeader struct {
	// VersionMinor selects the LAS minor version (1.x). 0 means automatic:
	// 4 for point formats 6-10, 2 otherwise. Point formats 4-5 require at
	// least 3; point formats 6-10 require at least 4. Maximum is 5.
	// LAS 1.0/1.1 output is not supported (0 selects a version automatically);
	// the closest writable version is 1.2.
	VersionMinor uint8

	// PointFormat is the LAS point data record format (0-10). Required.
	PointFormat uint8

	// ExtraByteCount is the number of extra bytes appended to every point
	// record beyond the format's base size. The total record length
	// (base + extra) must not exceed 1000 bytes.
	ExtraByteCount uint16

	// ScaleX, ScaleY, ScaleZ are the coordinate quantization scale factors.
	// Each zero value defaults to 0.001.
	ScaleX, ScaleY, ScaleZ float64

	// OffsetX, OffsetY, OffsetZ are the coordinate offsets.
	OffsetX, OffsetY, OffsetZ float64

	FileSourceID   uint16
	GlobalEncoding uint16

	// SystemIdentifier is the 32-char header field. Defaults to "golaz".
	SystemIdentifier string
	// GeneratingSoftware is the 32-char header field. Defaults to a golaz
	// version string.
	GeneratingSoftware string

	// FileCreationDayOfYear / FileCreationYear are written verbatim. When
	// both are zero they stay zero: the Writer never reads the system clock,
	// so identical inputs always produce identical files.
	FileCreationDayOfYear uint16
	FileCreationYear      uint16

	// NumberOfPoints optionally pre-declares the total point count. For
	// seekable outputs it may be left zero: the real count is patched into
	// the header on Close. For non-seekable outputs (see NewWriter) a
	// nonzero value is required whenever at least one point is written,
	// and Close errors if the actual count differs.
	NumberOfPoints uint64

	// VLRs are user Variable Length Records written verbatim after the
	// header. A LASzip VLR must not be supplied: it is generated
	// automatically for compressed output.
	VLRs []VLR

	// EVLRs are Extended Variable Length Records written after the point
	// data on Close. They require LAS 1.4+ (VersionMinor >= 4) and a
	// seekable output (the header's EVLR offset is only known once the
	// point data is finished). More records can be appended later with
	// Writer.AddEVLR, any time before Close.
	EVLRs []EVLR
}

// ---------------------------------------------------------------------------
// WriterOption — functional options for Create / NewWriter
// ---------------------------------------------------------------------------

// WriterOption is a functional option passed to Create or NewWriter to
// customise Writer behaviour.
type WriterOption func(*writerConfig)

// writerConfig holds the resolved options for a Writer.
type writerConfig struct {
	compression  *bool // nil → infer (Create: from extension; NewWriter: false)
	chunkSize    uint32
	chunkSizeSet bool
}

// WithCompression forces LAZ compression on (true) or off (false),
// overriding the default inference (Create infers from the file extension,
// NewWriter defaults to uncompressed).
func WithCompression(enabled bool) WriterOption {
	return func(cfg *writerConfig) {
		e := enabled
		cfg.compression = &e
	}
}

// WithChunkSize sets the number of points per compressed chunk.
// The default is 50000 (the LASzip default). Passing 0 selects
// variable-size chunking: chunk boundaries are then placed explicitly by
// calling Writer.Chunk(). The option is ignored for uncompressed output.
func WithChunkSize(n uint32) WriterOption {
	return func(cfg *writerConfig) {
		cfg.chunkSize = n
		cfg.chunkSizeSet = true
	}
}

// ---------------------------------------------------------------------------
// Writer — high-level LAS/LAZ writer
// ---------------------------------------------------------------------------

// Writer writes LAS and LAZ point cloud files.
//
// It is NOT safe for concurrent use by multiple goroutines: it maintains
// internal encode state (arithmetic coder, chunk boundaries, point buffers)
// that is mutated on every WritePoint call.
//
// WritePoint serializes the integer RawX/RawY/RawZ coordinates of a Point —
// the float64 X/Y/Z fields are NEVER re-quantized. Points obtained from a
// Reader carry both representations, so streaming Reader → Writer is
// lossless. For points constructed from real-world coordinates use
// SetCoordinates, which quantizes into RawX/RawY/RawZ using the Writer's
// scale and offset.
type Writer struct {
	out      io.Writer
	ownFile  *os.File // non-nil when opened by filename; closed by Close()
	stream   laz.ByteStreamOut
	seekable bool

	// resolved configuration
	hdr               WriterHeader // with defaults applied
	compressed        bool
	variableChunks    bool
	headerSize        uint16
	recordLen         uint16
	offsetToPointData uint32
	isPoint14         bool // pf 6-10

	// engine
	items   []laz.LASitem
	offsets []uint32 // in-memory offsets; offsets[len(items)] = total in-memory size
	wp      *laz.LASwritePoint

	// point encode buffers — pre-allocated once, reused every WritePoint()
	ptBuf      [][]byte // per-item slices into flatBuf
	flatBuf    []byte   // backing buffer in in-memory layout
	onDiskBuf  [30]byte // pf6-10 on-disk PDRF6 record staging buffer
	pt14conv   *laz.LASreadItemRawPoint14LE
	pt14stream *laz.ByteStreamInArray

	// EVLRs accumulated from the header config and AddEVLR; written after
	// the point data on Close.
	evlrs      []EVLR
	evlrOffset uint64 // file offset of the EVLR section, set during Close

	// inventory — updated on every WritePoint, patched into the header on
	// Close when the output is seekable
	count            uint64
	byReturn         [15]uint64
	minX, minY, minZ float64
	maxX, maxY, maxZ float64

	pointsSinceChunk uint64
	closed           bool
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// Create creates (or truncates) the named file and returns a Writer for it.
// Compression is inferred from the file extension — ".laz" (case-insensitive)
// produces a compressed file — unless overridden with WithCompression.
// The underlying file is closed by Close().
func Create(filename string, hdr WriterHeader, opts ...WriterOption) (*Writer, error) {
	cfg := applyWriterOptions(opts)
	if cfg.compression == nil {
		c := strings.EqualFold(filepath.Ext(filename), ".laz")
		cfg.compression = &c
	}
	f, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("create %q: %w", filename, err)
	}
	w, err := newWriter(f, true, hdr, cfg)
	if err != nil {
		f.Close()
		return nil, err
	}
	w.ownFile = f
	return w, nil
}

// NewWriter writes to an io.Writer. The caller retains ownership of the
// writer; Close() flushes golaz state but does not close it.
//
// If out also implements io.WriteSeeker (e.g. *os.File), the seekable path
// is used: the header's point count, per-return counts, and bounding box are
// patched on Close. Otherwise the header is written once up front, which
// requires WriterHeader.NumberOfPoints to be pre-declared (Close errors if
// points were written without it, or if the actual count differs); the
// bounding box and per-return counts are left zero — a documented
// limitation of non-seekable output.
//
// Output is uncompressed unless WithCompression(true) is given.
func NewWriter(out io.Writer, hdr WriterHeader, opts ...WriterOption) (*Writer, error) {
	cfg := applyWriterOptions(opts)
	if cfg.compression == nil {
		c := false
		cfg.compression = &c
	}
	_, seekable := out.(io.WriteSeeker)
	return newWriter(out, seekable, hdr, cfg)
}

func applyWriterOptions(opts []WriterOption) writerConfig {
	cfg := writerConfig{chunkSize: laz.LASZIP_CHUNK_SIZE_DEFAULT}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// pointFormatBaseSize returns the base point record length for a LAS point
// data format, and false if the format is unknown.
func pointFormatBaseSize(pf uint8) (uint16, bool) {
	switch pf {
	case 0:
		return 20, true
	case 1:
		return 28, true
	case 2:
		return 26, true
	case 3:
		return 34, true
	case 4:
		return 57, true
	case 5:
		return 63, true
	case 6:
		return 30, true
	case 7:
		return 36, true
	case 8:
		return 38, true
	case 9:
		return 59, true
	case 10:
		return 67, true
	}
	return 0, false
}

// resolveWriterHeader applies defaults and validates hdr in place.
// Returns the total point record length.
func resolveWriterHeader(hdr *WriterHeader) (uint16, error) {
	base, ok := pointFormatBaseSize(hdr.PointFormat)
	if !ok {
		return 0, fmt.Errorf("unsupported point format %d (must be 0-10)", hdr.PointFormat)
	}
	if hdr.VersionMinor == 0 {
		if hdr.PointFormat >= 6 {
			hdr.VersionMinor = 4
		} else {
			hdr.VersionMinor = 2
		}
	}
	if hdr.VersionMinor > 5 {
		return 0, fmt.Errorf("unsupported LAS version 1.%d (maximum 1.5)", hdr.VersionMinor)
	}
	if hdr.PointFormat >= 6 && hdr.VersionMinor < 4 {
		return 0, fmt.Errorf("point format %d requires LAS 1.4 or later (got 1.%d)", hdr.PointFormat, hdr.VersionMinor)
	}
	if (hdr.PointFormat == 4 || hdr.PointFormat == 5) && hdr.VersionMinor < 3 {
		return 0, fmt.Errorf("point format %d requires LAS 1.3 or later (got 1.%d)", hdr.PointFormat, hdr.VersionMinor)
	}
	recordLen := int(base) + int(hdr.ExtraByteCount)
	if recordLen > 1000 {
		return 0, fmt.Errorf("point record length %d exceeds the 1000-byte limit (%d base + %d extra)",
			recordLen, base, hdr.ExtraByteCount)
	}
	if hdr.ScaleX == 0 {
		hdr.ScaleX = defaultScale
	}
	if hdr.ScaleY == 0 {
		hdr.ScaleY = defaultScale
	}
	if hdr.ScaleZ == 0 {
		hdr.ScaleZ = defaultScale
	}
	if hdr.SystemIdentifier == "" {
		hdr.SystemIdentifier = defaultSystemIdentifier
	}
	if hdr.GeneratingSoftware == "" {
		hdr.GeneratingSoftware = defaultGeneratingSoftware
	}
	if len(hdr.SystemIdentifier) > 32 {
		return 0, fmt.Errorf("SystemIdentifier %q exceeds 32 bytes", hdr.SystemIdentifier)
	}
	if len(hdr.GeneratingSoftware) > 32 {
		return 0, fmt.Errorf("GeneratingSoftware %q exceeds 32 bytes", hdr.GeneratingSoftware)
	}
	if hdr.VersionMinor < 4 && hdr.NumberOfPoints > math.MaxUint32 {
		return 0, fmt.Errorf("LAS 1.%d cannot declare %d points (uint32 limit; use LAS 1.4)",
			hdr.VersionMinor, hdr.NumberOfPoints)
	}
	for i, v := range hdr.VLRs {
		if v.IsLASzip() {
			return 0, fmt.Errorf("VLR[%d]: the LASzip VLR is written automatically for compressed output and must not be supplied", i)
		}
		if len(v.UserID) > 16 {
			return 0, fmt.Errorf("VLR[%d]: UserID %q exceeds 16 bytes", i, v.UserID)
		}
		if len(v.Description) > 32 {
			return 0, fmt.Errorf("VLR[%d]: Description %q exceeds 32 bytes", i, v.Description)
		}
		if len(v.Data) > math.MaxUint16 {
			return 0, fmt.Errorf("VLR[%d]: payload of %d bytes exceeds the 65535-byte VLR limit", i, len(v.Data))
		}
	}
	if len(hdr.EVLRs) > 0 && hdr.VersionMinor < 4 {
		return 0, fmt.Errorf("EVLRs require LAS 1.4 or later (got 1.%d)", hdr.VersionMinor)
	}
	for i, e := range hdr.EVLRs {
		if err := validateEVLR(e); err != nil {
			return 0, fmt.Errorf("EVLR[%d]: %w", i, err)
		}
	}
	return uint16(recordLen), nil
}

// validateEVLR checks the fixed-size string fields of an EVLR.
func validateEVLR(e EVLR) error {
	if len(e.UserID) > 16 {
		return fmt.Errorf("UserID %q exceeds 16 bytes", e.UserID)
	}
	if len(e.Description) > 32 {
		return fmt.Errorf("Description %q exceeds 32 bytes", e.Description)
	}
	return nil
}

// headerSizeForMinor returns the public header block size for a LAS 1.x
// minor version.
func headerSizeForMinor(minor uint8) uint16 {
	switch {
	case minor >= 4:
		return 375
	case minor == 3:
		return 235
	default:
		return 227
	}
}

// newWriter is the shared construction body.
func newWriter(out io.Writer, seekable bool, hdr WriterHeader, cfg writerConfig) (*Writer, error) {
	recordLen, err := resolveWriterHeader(&hdr)
	if err != nil {
		return nil, err
	}

	if len(hdr.EVLRs) > 0 && !seekable {
		return nil, fmt.Errorf("EVLRs require a seekable output: the header's EVLR offset is only known after the point data")
	}

	w := &Writer{
		out:        out,
		seekable:   seekable,
		hdr:        hdr,
		compressed: *cfg.compression,
		headerSize: headerSizeForMinor(hdr.VersionMinor),
		recordLen:  recordLen,
		isPoint14:  hdr.PointFormat >= 6,
		evlrs:      append([]EVLR(nil), hdr.EVLRs...),
		minX:       math.Inf(1), minY: math.Inf(1), minZ: math.Inf(1),
		maxX: math.Inf(-1), maxY: math.Inf(-1), maxZ: math.Inf(-1),
	}

	// ── 1. Build the LASzip configuration ────────────────────────────────
	compressor := uint16(laz.LASZIP_COMPRESSOR_NONE)
	if w.compressed {
		if w.isPoint14 {
			compressor = laz.LASZIP_COMPRESSOR_LAYERED_CHUNKED
		} else {
			compressor = laz.LASZIP_COMPRESSOR_POINTWISE_CHUNKED
		}
	}
	lz := laz.NewLASzip()
	if err := lz.SetupByPointType(hdr.PointFormat, recordLen, compressor); err != nil {
		return nil, fmt.Errorf("setup LASzip config: %w", err)
	}
	var lzPayload []byte
	if w.compressed {
		if err := lz.RequestVersion(laz.GetDefaultVersion(hdr.PointFormat, 1, hdr.VersionMinor)); err != nil {
			return nil, fmt.Errorf("request item versions: %w", err)
		}
		if cfg.chunkSizeSet && cfg.chunkSize == 0 {
			w.variableChunks = true
		}
		// ChunkSize 0 in the LASzip VLR selects variable-size chunking.
		if err := lz.SetChunkSize(cfg.chunkSize); err != nil {
			return nil, fmt.Errorf("set chunk size: %w", err)
		}
		lzPayload, err = lz.Pack()
		if err != nil {
			return nil, fmt.Errorf("pack LASzip VLR: %w", err)
		}
	}

	// ── 2. Item list and in-memory offsets ───────────────────────────────
	w.items = make([]laz.LASitem, lz.NumItems)
	copy(w.items, lz.Items)
	w.offsets = make([]uint32, len(w.items)+1)
	off := uint32(0)
	for i, item := range w.items {
		w.offsets[i] = off
		sz := uint32(item.Size)
		if item.Type == laz.LASITEM_POINT14 {
			sz = 40 // expanded in-memory layout
		}
		off += sz
	}
	w.offsets[len(w.items)] = off

	// ── 3. Serialize header + VLRs ────────────────────────────────────────
	vlrBytes := buildVLRBlock(hdr.VLRs, lzPayload)
	offsetToPointData := uint64(w.headerSize) + uint64(len(vlrBytes))
	if offsetToPointData > math.MaxUint32 {
		return nil, fmt.Errorf("VLR section too large: offset to point data %d exceeds uint32", offsetToPointData)
	}
	w.offsetToPointData = uint32(offsetToPointData)

	if seekable {
		ws, ok := out.(io.WriteSeeker)
		if !ok {
			return nil, fmt.Errorf("internal error: seekable writer does not implement io.WriteSeeker")
		}
		w.stream = laz.NewByteStreamOutFile(ws)
	} else {
		w.stream = laz.NewByteStreamOutWriter(out)
	}

	headerBuf := w.headerBytes(hdr.NumberOfPoints, [15]uint64{}, [6]float64{})
	if err := w.stream.PutBytes(headerBuf); err != nil {
		return nil, fmt.Errorf("write header: %w", err)
	}
	if err := w.stream.PutBytes(vlrBytes); err != nil {
		return nil, fmt.Errorf("write VLRs: %w", err)
	}

	// ── 4. Set up the write engine ────────────────────────────────────────
	w.wp = laz.NewLASwritePoint()
	if err := w.wp.Setup(uint32(lz.NumItems), w.items, lz); err != nil {
		return nil, fmt.Errorf("setup LASwritePoint: %w", err)
	}
	if err := w.wp.Init(w.stream); err != nil {
		return nil, fmt.Errorf("init LASwritePoint: %w", err)
	}

	// ── 5. Allocate point encode buffers ──────────────────────────────────
	w.flatBuf = make([]byte, w.offsets[len(w.items)])
	w.ptBuf = make([][]byte, len(w.items))
	for i := range w.items {
		w.ptBuf[i] = w.flatBuf[w.offsets[i]:w.offsets[i+1]]
	}
	if w.isPoint14 {
		// The 30→40-byte conversion reuses the engine's raw POINT14 reader so
		// legacy-field derivation (return clamping, scan-angle rank, the
		// extended_point_type bit) stays canonical with the read path.
		w.pt14stream = laz.NewByteStreamInArray(nil)
		w.pt14conv = &laz.LASreadItemRawPoint14LE{}
		if err := w.pt14conv.Init(w.pt14stream); err != nil {
			return nil, fmt.Errorf("init POINT14 converter: %w", err)
		}
	}

	return w, nil
}

// ---------------------------------------------------------------------------
// Header / VLR serialization
// ---------------------------------------------------------------------------

// headerBytes serializes the public header block (the exact inverse of
// parseHeader for this version's header size). bbox is in on-disk order:
// [MaxX, MinX, MaxY, MinY, MaxZ, MinZ].
func (w *Writer) headerBytes(count uint64, byReturn [15]uint64, bbox [6]float64) []byte {
	h := &w.hdr
	buf := make([]byte, w.headerSize)
	copy(buf[0:4], "LASF")
	binary.LittleEndian.PutUint16(buf[4:6], h.FileSourceID)
	binary.LittleEndian.PutUint16(buf[6:8], h.GlobalEncoding)
	// Project ID GUID (bytes 8-23) left zero.
	buf[24] = 1 // version major
	buf[25] = h.VersionMinor
	copy(buf[26:58], h.SystemIdentifier)
	copy(buf[58:90], h.GeneratingSoftware)
	binary.LittleEndian.PutUint16(buf[90:92], h.FileCreationDayOfYear)
	binary.LittleEndian.PutUint16(buf[92:94], h.FileCreationYear)
	binary.LittleEndian.PutUint16(buf[94:96], w.headerSize)
	binary.LittleEndian.PutUint32(buf[96:100], w.offsetToPointData)
	numVLRs := uint32(len(h.VLRs))
	if w.compressed {
		numVLRs++
	}
	binary.LittleEndian.PutUint32(buf[100:104], numVLRs)
	pdf := h.PointFormat
	if w.compressed {
		pdf |= 0x80
	}
	buf[104] = pdf
	binary.LittleEndian.PutUint16(buf[105:107], w.recordLen)

	// Legacy point counts: populated only when representable (pf 0-5 and
	// count < 2^32); zero otherwise, per the LAS 1.4 spec. For LAS < 1.4
	// these are the only counts (resolveWriterHeader/Close guarantee fit).
	if h.PointFormat <= 5 && count <= math.MaxUint32 {
		binary.LittleEndian.PutUint32(buf[107:111], uint32(count))
		for i := range 5 {
			v := byReturn[i]
			if v > math.MaxUint32 {
				v = 0
			}
			binary.LittleEndian.PutUint32(buf[111+i*4:115+i*4], uint32(v))
		}
	}

	binary.LittleEndian.PutUint64(buf[131:139], math.Float64bits(h.ScaleX))
	binary.LittleEndian.PutUint64(buf[139:147], math.Float64bits(h.ScaleY))
	binary.LittleEndian.PutUint64(buf[147:155], math.Float64bits(h.ScaleZ))
	binary.LittleEndian.PutUint64(buf[155:163], math.Float64bits(h.OffsetX))
	binary.LittleEndian.PutUint64(buf[163:171], math.Float64bits(h.OffsetY))
	binary.LittleEndian.PutUint64(buf[171:179], math.Float64bits(h.OffsetZ))
	for i, v := range bbox {
		binary.LittleEndian.PutUint64(buf[179+i*8:187+i*8], math.Float64bits(v))
	}

	// LAS 1.3+: start of waveform data packet record (unsupported → 0).
	// LAS 1.4+: EVLR count/offset (zero until the EVLR section is written on
	// Close, then patched), extended counts.
	if h.VersionMinor >= 4 {
		if w.evlrOffset != 0 {
			binary.LittleEndian.PutUint32(buf[235:239], uint32(len(w.evlrs)))
			binary.LittleEndian.PutUint64(buf[239:247], w.evlrOffset)
		}
		binary.LittleEndian.PutUint64(buf[247:255], count)
		for i := range 15 {
			binary.LittleEndian.PutUint64(buf[255+i*8:263+i*8], byReturn[i])
		}
	}
	return buf
}

// buildVLRBlock serializes the user VLRs followed by the LASzip VLR (when
// lzPayload is non-nil, i.e. compressed output).
func buildVLRBlock(vlrs []VLR, lzPayload []byte) []byte {
	var buf []byte
	for _, v := range vlrs {
		buf = appendVLR(buf, v.UserID, v.RecordID, v.Description, v.Data)
	}
	if lzPayload != nil {
		buf = appendVLR(buf, "laszip encoded", 22204, laszipVLRDescription, lzPayload)
	}
	return buf
}

// appendVLR appends one 54-byte VLR header plus payload to buf.
func appendVLR(buf []byte, userID string, recID uint16, desc string, data []byte) []byte {
	var h [54]byte
	// h[0:2] reserved = 0
	copy(h[2:18], userID)
	binary.LittleEndian.PutUint16(h[18:20], recID)
	binary.LittleEndian.PutUint16(h[20:22], uint16(len(data)))
	copy(h[22:54], desc)
	buf = append(buf, h[:]...)
	return append(buf, data...)
}

// appendEVLR appends one 60-byte EVLR header plus payload to buf
// (2 reserved + 16 userID + 2 recID + 8 payload length + 32 description).
func appendEVLR(buf []byte, e EVLR) []byte {
	var h [60]byte
	copy(h[2:18], e.UserID)
	binary.LittleEndian.PutUint16(h[18:20], e.RecordID)
	binary.LittleEndian.PutUint64(h[20:28], uint64(len(e.Data)))
	copy(h[28:60], e.Description)
	buf = append(buf, h[:]...)
	return append(buf, e.Data...)
}

// AddEVLR queues an Extended Variable Length Record to be written after the
// point data when the Writer is closed. It can be called any time before
// Close — useful for records whose content is only known once all points
// are written (e.g. spatial index structures). Requires LAS 1.4+ and a
// seekable output, like WriterHeader.EVLRs.
func (w *Writer) AddEVLR(e EVLR) error {
	if w.closed {
		return fmt.Errorf("add EVLR: writer is closed")
	}
	if w.hdr.VersionMinor < 4 {
		return fmt.Errorf("add EVLR: EVLRs require LAS 1.4 or later (writing 1.%d)", w.hdr.VersionMinor)
	}
	if !w.seekable {
		return fmt.Errorf("add EVLR: EVLRs require a seekable output")
	}
	if err := validateEVLR(e); err != nil {
		return fmt.Errorf("add EVLR: %w", err)
	}
	w.evlrs = append(w.evlrs, e)
	return nil
}

// ---------------------------------------------------------------------------
// Point writing
// ---------------------------------------------------------------------------

// WritePoint encodes one point.
//
// The on-disk X/Y/Z come from p.RawX/RawY/RawZ — the float64 p.X/Y/Z fields
// are NEVER re-quantized, so a point streamed from a Reader round-trips
// bit-exactly regardless of scale/offset precision. Use SetCoordinates to
// quantize real-world coordinates into RawX/RawY/RawZ first when
// constructing points from scratch.
//
// Fields that do not exist in the Writer's point format are ignored;
// unset optional fields are written as zero. Extra bytes are zero-padded or
// truncated to the header's ExtraByteCount.
//
// Zero allocations on the hot path for point formats 0-5; formats 6-10
// perform one small fixed-size allocation per point inside the shared
// record converter.
func (w *Writer) WritePoint(p *Point) error {
	if w.closed {
		return fmt.Errorf("write point: writer is closed")
	}
	if p == nil {
		return fmt.Errorf("write point: nil point")
	}
	if err := w.encodeItems(p); err != nil {
		return fmt.Errorf("encode point %d: %w", w.count, err)
	}
	if err := w.wp.Write(w.ptBuf); err != nil {
		return fmt.Errorf("write point %d: %w", w.count, err)
	}
	w.count++
	w.pointsSinceChunk++

	// Inventory: per-return counts and bounding box of scaled coordinates.
	rn := p.ReturnNumber
	if w.isPoint14 {
		rn &= 0x0F
	} else {
		rn &= 0x07
	}
	if rn >= 1 {
		w.byReturn[rn-1]++
	}
	x := float64(p.RawX)*w.hdr.ScaleX + w.hdr.OffsetX
	y := float64(p.RawY)*w.hdr.ScaleY + w.hdr.OffsetY
	z := float64(p.RawZ)*w.hdr.ScaleZ + w.hdr.OffsetZ
	w.minX = math.Min(w.minX, x)
	w.maxX = math.Max(w.maxX, x)
	w.minY = math.Min(w.minY, y)
	w.maxY = math.Max(w.maxY, y)
	w.minZ = math.Min(w.minZ, z)
	w.maxZ = math.Max(w.maxZ, z)
	return nil
}

// encodeItems fills w.ptBuf with the per-item in-memory buffers for p.
func (w *Writer) encodeItems(p *Point) error {
	if w.isPoint14 {
		// Build the 30-byte on-disk PDRF6 record, then expand it to the
		// 40-byte in-memory POINT14 layout via the engine's raw reader.
		b := w.onDiskBuf[:]
		binary.LittleEndian.PutUint32(b[0:4], uint32(p.RawX))
		binary.LittleEndian.PutUint32(b[4:8], uint32(p.RawY))
		binary.LittleEndian.PutUint32(b[8:12], uint32(p.RawZ))
		binary.LittleEndian.PutUint16(b[12:14], p.Intensity)
		b[14] = (p.ReturnNumber & 0x0F) | ((p.NumberOfReturns & 0x0F) << 4)
		b[15] = (p.ClassificationFlags & 0x0F) |
			((p.scannerChannel & 0x03) << 4) |
			(boolBit(p.ScanDirectionFlag) << 6) |
			(boolBit(p.EdgeOfFlightLine) << 7)
		b[16] = p.Classification
		b[17] = p.UserData
		binary.LittleEndian.PutUint16(b[18:20], uint16(scanAngleI16FromDegrees(p.ScanAngleDegrees)))
		binary.LittleEndian.PutUint16(b[20:22], p.PointSourceID)
		binary.LittleEndian.PutUint64(b[22:30], math.Float64bits(p.gpsTime))

		w.pt14stream.Init(b)
		ctx := uint32(0)
		if err := w.pt14conv.Read(w.ptBuf[0], &ctx); err != nil {
			return fmt.Errorf("convert POINT14 record: %w", err)
		}
	} else {
		// POINT10: 20-byte on-disk record, identical in-memory (exact
		// inverse of populatePoint10).
		b := w.ptBuf[0]
		binary.LittleEndian.PutUint32(b[0:4], uint32(p.RawX))
		binary.LittleEndian.PutUint32(b[4:8], uint32(p.RawY))
		binary.LittleEndian.PutUint32(b[8:12], uint32(p.RawZ))
		binary.LittleEndian.PutUint16(b[12:14], p.Intensity)
		b[14] = (p.ReturnNumber & 0x07) |
			((p.NumberOfReturns & 0x07) << 3) |
			(boolBit(p.ScanDirectionFlag) << 6) |
			(boolBit(p.EdgeOfFlightLine) << 7)
		b[15] = (p.Classification & 0x1F) | ((p.ClassificationFlags & 0x07) << 5)
		b[16] = byte(scanAngleRankFromDegrees(p.ScanAngleDegrees))
		b[17] = p.UserData
		binary.LittleEndian.PutUint16(b[18:20], p.PointSourceID)
	}

	for idx := 1; idx < len(w.items); idx++ {
		itemBuf := w.ptBuf[idx]
		switch w.items[idx].Type {
		case laz.LASITEM_GPSTIME11:
			binary.LittleEndian.PutUint64(itemBuf[0:8], math.Float64bits(p.gpsTime))

		case laz.LASITEM_RGB12, laz.LASITEM_RGB14:
			binary.LittleEndian.PutUint16(itemBuf[0:2], p.red)
			binary.LittleEndian.PutUint16(itemBuf[2:4], p.green)
			binary.LittleEndian.PutUint16(itemBuf[4:6], p.blue)

		case laz.LASITEM_RGBNIR14:
			binary.LittleEndian.PutUint16(itemBuf[0:2], p.red)
			binary.LittleEndian.PutUint16(itemBuf[2:4], p.green)
			binary.LittleEndian.PutUint16(itemBuf[4:6], p.blue)
			binary.LittleEndian.PutUint16(itemBuf[6:8], p.nir)

		case laz.LASITEM_WAVEPACKET13, laz.LASITEM_WAVEPACKET14:
			itemBuf[0] = p.waveIdx
			binary.LittleEndian.PutUint64(itemBuf[1:9], p.waveOffset)
			binary.LittleEndian.PutUint32(itemBuf[9:13], p.waveSize)
			binary.LittleEndian.PutUint32(itemBuf[13:17], math.Float32bits(p.waveLoc))
			binary.LittleEndian.PutUint32(itemBuf[17:21], math.Float32bits(p.waveXt))
			binary.LittleEndian.PutUint32(itemBuf[21:25], math.Float32bits(p.waveYt))
			binary.LittleEndian.PutUint32(itemBuf[25:29], math.Float32bits(p.waveZt))

		case laz.LASITEM_BYTE, laz.LASITEM_BYTE14:
			n := copy(itemBuf, p.extraBuf)
			clear(itemBuf[n:])
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Navigation / helpers
// ---------------------------------------------------------------------------

// Tell returns the number of points written so far.
func (w *Writer) Tell() uint64 { return w.count }

// Chunk closes the current compressed chunk at an explicit boundary.
// Only valid for compressed output with WithChunkSize(0) (variable-size
// chunking). Calling Chunk when no points were written since the previous
// boundary is a no-op.
func (w *Writer) Chunk() error {
	if w.closed {
		return fmt.Errorf("chunk: writer is closed")
	}
	if !w.compressed || !w.variableChunks {
		return fmt.Errorf("chunk: explicit chunk boundaries require compressed output with WithChunkSize(0)")
	}
	if w.pointsSinceChunk == 0 {
		return nil
	}
	if err := w.wp.Chunk(); err != nil {
		return fmt.Errorf("chunk: %w", err)
	}
	w.pointsSinceChunk = 0
	return nil
}

// SetCoordinates quantizes the real-world coordinates x, y, z into
// p.RawX/RawY/RawZ using the Writer's scale and offset — rounding half away
// from zero, exactly like the C++ I32_QUANTIZE macro — and stores x, y, z
// into p.X/Y/Z. WritePoint serializes the Raw values, so this is the helper
// to call when building points from scratch.
func (w *Writer) SetCoordinates(p *Point, x, y, z float64) {
	p.RawX = i32Quantize((x - w.hdr.OffsetX) / w.hdr.ScaleX)
	p.RawY = i32Quantize((y - w.hdr.OffsetY) / w.hdr.ScaleY)
	p.RawZ = i32Quantize((z - w.hdr.OffsetZ) / w.hdr.ScaleZ)
	p.X, p.Y, p.Z = x, y, z
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

// Close finalizes the file: it flushes the compression engine (writing the
// chunk table for compressed output) and, when the output is seekable,
// patches the header with the final point count, per-return counts, and
// bounding box. If the Writer was created with Create, the underlying file
// is closed. Close is idempotent; calls after the first return nil.
//
// For non-seekable output the header cannot be patched: Close returns an
// error if points were written without pre-declaring
// WriterHeader.NumberOfPoints, or if the declared count does not match the
// number of points actually written.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if err := w.wp.Done(); err != nil {
		record(fmt.Errorf("finalize point data: %w", err))
	}

	if w.hdr.VersionMinor < 4 && w.count > math.MaxUint32 {
		record(fmt.Errorf("close: %d points exceed the LAS 1.%d uint32 limit", w.count, w.hdr.VersionMinor))
	}

	// EVLR section: written after the point data (and chunk table); the
	// header's EVLR count/offset are patched below. Constructor/AddEVLR
	// guarantee LAS 1.4+ and a seekable output whenever w.evlrs is non-empty.
	if firstErr == nil && len(w.evlrs) > 0 {
		pos, err := w.stream.Tell()
		if err != nil {
			record(fmt.Errorf("close: locate EVLR section: %w", err))
		} else {
			w.evlrOffset = uint64(pos)
			var buf []byte
			for _, e := range w.evlrs {
				buf = appendEVLR(buf, e)
			}
			if err := w.stream.PutBytes(buf); err != nil {
				record(fmt.Errorf("close: write EVLRs: %w", err))
			}
		}
	}

	if firstErr == nil {
		if w.seekable {
			record(w.patchHeader())
		} else if w.count != w.hdr.NumberOfPoints {
			if w.hdr.NumberOfPoints == 0 {
				record(fmt.Errorf("close: wrote %d points to a non-seekable output without pre-declaring WriterHeader.NumberOfPoints; the header cannot be patched", w.count))
			} else {
				record(fmt.Errorf("close: wrote %d points but the header declares %d; the output is not seekable so the header cannot be patched", w.count, w.hdr.NumberOfPoints))
			}
		}
	}

	if w.ownFile != nil {
		record(w.ownFile.Close())
		w.ownFile = nil
	}
	return firstErr
}

// patchHeader rewrites the public header block with the final inventory.
func (w *Writer) patchHeader() error {
	var bbox [6]float64
	if w.count > 0 {
		bbox = [6]float64{w.maxX, w.minX, w.maxY, w.minY, w.maxZ, w.minZ}
	}
	pos, err := w.stream.Tell()
	if err != nil {
		return fmt.Errorf("patch header: tell: %w", err)
	}
	buf := w.headerBytes(w.count, w.byReturn, bbox)
	if err := w.stream.Seek(0); err != nil {
		return fmt.Errorf("patch header: seek: %w", err)
	}
	if err := w.stream.PutBytes(buf); err != nil {
		return fmt.Errorf("patch header: write: %w", err)
	}
	if err := w.stream.Seek(pos); err != nil {
		return fmt.Errorf("patch header: restore position: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Quantization helpers
// ---------------------------------------------------------------------------

// boolBit converts a bool to 0/1.
func boolBit(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// i32Quantize rounds half away from zero and truncates to int32, exactly
// like the C++ I32_QUANTIZE macro applied to a float64 operand.
func i32Quantize(v float64) int32 {
	if v >= 0 {
		return int32(v + 0.5)
	}
	return int32(v - 0.5)
}

// scanAngleRankFromDegrees converts a scan angle in degrees to the int8
// scan_angle_rank stored by point formats 0-5. Rounds half away from zero
// and clamps to [-128, 127]. Exact inverse of the Reader's
// ScanAngleDegrees = float64(int8(rank)).
func scanAngleRankFromDegrees(deg float64) int8 {
	var q float64
	if deg >= 0 {
		q = deg + 0.5
	} else {
		q = deg - 0.5
	}
	if q >= 127 {
		return 127
	}
	if q <= -128 {
		return -128
	}
	return int8(q)
}

// scanAngleI16FromDegrees converts a scan angle in degrees to the int16
// 0.006-degree increments stored by point formats 6-10. The quantization
// happens in float32 with round-half-away-from-zero, mirroring the C++
// I16_QUANTIZE macro, then clamps to the int16 range. Exact inverse of the
// Reader's ScanAngleDegrees = float64(int16)*0.006.
func scanAngleI16FromDegrees(deg float64) int16 {
	v := float32(deg / 0.006)
	var q float32
	if v >= 0 {
		q = v + 0.5
	} else {
		q = v - 0.5
	}
	if q >= math.MaxInt16 {
		return math.MaxInt16
	}
	if q <= math.MinInt16 {
		return math.MinInt16
	}
	return int16(q)
}
