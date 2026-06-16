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
	"fmt"
	"io"
	"os"
	"strings"

	laz "github.com/mfbonfigli/golaz/internal/laz"
)

// ---------------------------------------------------------------------------
// Reader — high-level LAS/LAZ reader
// ---------------------------------------------------------------------------

// Reader reads LAS and LAZ point cloud files.
//
// It is NOT safe for concurrent use by multiple goroutines. It maintains
// internal decode state (arithmetic coder position, chunk boundaries, point
// buffers) that is mutated on every Scan/Next/Seek call. Callers must either
// open a separate Reader per goroutine or serialize access to a shared Reader
// with a mutex.
type Reader struct {
	// source
	rs      io.ReadSeeker
	ownFile *os.File // non-nil when opened by filename; closed by Close()

	// parsed at Open time
	header  *Header
	vlrs    []VLR
	items   []laz.LASitem
	offsets []uint32    // in-memory offsets; offsets[len(items)] = total in-memory size
	lz      *laz.LASzip // nil for uncompressed files

	// decompression engine
	lp     *laz.LASreadPoint
	stream *laz.ByteStreamInReader

	// extra-byte descriptor support
	extraByteDescs []ExtraByteDescriptor
	extraByteIndex map[string]int // name → index into extraByteDescs
	extraByteCount uint32

	// point decode buffers — pre-allocated once, reused every Scan()
	ptBuf     [][]byte // per-item slices into flatBuf
	flatBuf   []byte   // backing buffer in in-memory layout
	onDiskBuf []byte   // backing buffer in on-disk layout; used by Raw() for pf6–10

	// derived from header
	scaleX, scaleY, scaleZ    float64
	offsetX, offsetY, offsetZ float64
	present                   pointPresent // precomputed from format + extraByteCount
	isPoint14                 bool         // pf6–10

	// read state
	pointCount  uint64
	totalPoints uint64

	// EVLR cache
	evlrs       []EVLR
	evlrsLoaded bool
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

// Open opens a LAS or LAZ file by path.
// The underlying file is kept open until Close() is called.
func Open(filename string, opts ...Option) (*Reader, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", filename, err)
	}
	r, err := openFrom(f, opts...)
	if err != nil {
		f.Close()
		return nil, err
	}
	r.ownFile = f
	return r, nil
}

// OpenReader reads from an io.ReadSeeker.
// The caller retains ownership of the reader and is responsible for closing it.
// Seek() and EVLRs() require the underlying reader to support seeking, which is
// the case for *os.File and *bytes.Reader. Non-seekable sources are not accepted;
// wrap them in a bytes.Buffer or temporary file first.
func OpenReader(rs io.ReadSeeker, opts ...Option) (*Reader, error) {
	return openFrom(rs, opts...)
}

// openFrom is the shared construction body.
func openFrom(rs io.ReadSeeker, opts ...Option) (*Reader, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	r := &Reader{rs: rs}
	r.stream = laz.NewByteStreamInReader(rs)

	// ── 1. Parse header ──────────────────────────────────────────────────
	h, err := parseHeader(rs)
	if err != nil {
		return nil, err
	}
	r.header = h
	r.totalPoints = h.NumberOfPoints
	r.scaleX, r.scaleY, r.scaleZ = h.ScaleX, h.ScaleY, h.ScaleZ
	r.offsetX, r.offsetY, r.offsetZ = h.OffsetX, h.OffsetY, h.OffsetZ

	// ── 2. Read all VLRs ─────────────────────────────────────────────────
	vlrs, laszipData, err := readAllVLRs(rs, h.HeaderSize, h.NumberOfVLRs)
	if err != nil {
		return nil, err
	}
	r.vlrs = vlrs

	// ── 3. Unpack LASzip VLR (if present) ────────────────────────────────
	if laszipData != nil {
		lz := laz.NewLASzip()
		if err := lz.Unpack(laszipData); err != nil {
			return nil, fmt.Errorf("unpack LASzip VLR: %w", err)
		}
		r.lz = lz
		r.header.IsCompressed = true
	}

	// ── 4. Build LASzip config (or raw config for uncompressed) ──────────
	var lzCfg *laz.LASzip
	if r.lz != nil {
		lzCfg = r.lz
		if len(lzCfg.Items) == 0 {
			return nil, fmt.Errorf("LASzip VLR has no items")
		}
	} else {
		lzCfg = laz.NewLASzip()
		if err := lzCfg.SetupByPointType(
			h.PointDataFormat, h.PointDataRecordLength, laz.LASZIP_COMPRESSOR_NONE,
		); err != nil {
			return nil, fmt.Errorf("setup raw LAS config: %w", err)
		}
	}

	// ── 5. Build item list and in-memory offsets ──────────────────────────
	r.items = make([]laz.LASitem, lzCfg.NumItems)
	copy(r.items, lzCfg.Items)

	r.offsets = make([]uint32, lzCfg.NumItems+1)
	off := uint32(0)
	for i := uint16(0); i < lzCfg.NumItems; i++ {
		r.offsets[i] = off
		sz := uint32(r.items[i].Size)
		if r.items[i].Type == laz.LASITEM_POINT14 {
			sz = 40 // expanded in-memory layout
		}
		off += sz
	}
	r.offsets[lzCfg.NumItems] = off

	// ── 6. Detect extra bytes and point capabilities ──────────────────────
	for _, item := range r.items {
		if item.Type == laz.LASITEM_BYTE || item.Type == laz.LASITEM_BYTE14 {
			r.extraByteCount += uint32(item.Size)
		}
	}
	r.present = pointPresentFor(h.PointDataFormat, r.extraByteCount)
	r.isPoint14 = h.PointDataFormat >= 6

	// ── 7. Parse ExtraByteDescriptor VLR if present ───────────────────────
	for _, v := range vlrs {
		if v.IsExtraByteDescriptor() {
			descs, err := v.ExtraByteDescriptors()
			if err != nil {
				return nil, fmt.Errorf("parse ExtraByteDescriptor VLR: %w", err)
			}
			// Soft validation: total size of descriptors should equal the actual
			// extra bytes in the point record. Real-world files sometimes use
			// deprecated array types (type 0 = undocumented, types 11-29) whose
			// byte counts are ambiguous, causing mismatches. When sizes disagree
			// we skip named access (ExtraByte by name returns an error) but raw
			// access via ExtraBytes() still works.
			total := uint32(0)
			for _, d := range descs {
				total += uint32(d.ByteSize)
			}
			if total != r.extraByteCount {
				// Silently skip — descriptors are unusable but the file is still readable.
				break
			}
			// Assign byte offsets within the extra-bytes section.
			byteOff := uint16(0)
			for i := range descs {
				descs[i].ByteOffset = byteOff
				byteOff += uint16(descs[i].ByteSize)
			}
			r.extraByteDescs = descs
			r.extraByteIndex = make(map[string]int, len(descs))
			for i, d := range descs {
				r.extraByteIndex[d.Name] = i
			}
			break
		}
	}

	// ── 8. Set up LASreadPoint ────────────────────────────────────────────
	mask := cfg.selectiveMask
	if !cfg.maskExplicitlySet {
		mask = SelectiveAll
	}
	r.lp = laz.NewLASreadPoint(uint32(mask))
	if err := r.lp.Setup(uint32(lzCfg.NumItems), r.items, lzCfg); err != nil {
		return nil, fmt.Errorf("setup LASreadPoint: %w", err)
	}

	// ── 9. Allocate point decode buffers ──────────────────────────────────
	totalInMem := r.offsets[lzCfg.NumItems]
	r.flatBuf = make([]byte, totalInMem)
	r.ptBuf = make([][]byte, lzCfg.NumItems)
	for i := uint16(0); i < lzCfg.NumItems; i++ {
		r.ptBuf[i] = r.flatBuf[r.offsets[i]:r.offsets[i+1]]
	}
	r.onDiskBuf = make([]byte, h.PointDataRecordLength)

	// ── 10. Seek to point data and initialise decoder ─────────────────────
	if _, err := rs.Seek(int64(h.OffsetToPointData), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to point data: %w", err)
	}
	// Reset the bufio buffer after the seek.
	r.stream = laz.NewByteStreamInReader(rs)
	if err := r.lp.Init(r.stream); err != nil {
		return nil, fmt.Errorf("init LASreadPoint: %w", err)
	}

	return r, nil
}

// ---------------------------------------------------------------------------
// Metadata accessors
// ---------------------------------------------------------------------------

// Header returns the parsed LAS public header. Never nil after Open.
func (r *Reader) Header() *Header { return r.header }

// WKT returns the OGC WKT coordinate reference system extracted from
// LASF_Projection VLRs (recID 2112 = CoordinateSystem, recID 2111 = MathTransform).
// Both VLRs and EVLRs are scanned. Returns nil if no WKT records are found.
// EVLRs are loaded on demand if the file is LAS 1.4+; an unreachable EVLR
// section is silently ignored.
func (r *Reader) WKT() *WKT {
	w := &WKT{}

	apply := func(userID string, recID uint16, data []byte) {
		if userID != "LASF_Projection" {
			return
		}
		switch recID {
		case 2111:
			w.MathTransform = strings.TrimRight(string(data), "\x00")
		case 2112:
			w.CoordinateSystem = strings.TrimRight(string(data), "\x00")
		}
	}

	for _, v := range r.vlrs {
		apply(v.UserID, v.RecordID, v.Data)
	}
	// Load EVLRs if available; ignore errors silently (best-effort).
	evlrs, _ := r.EVLRs()
	for _, e := range evlrs {
		apply(e.UserID, e.RecordID, e.Data)
	}

	if w.CoordinateSystem == "" && w.MathTransform == "" {
		return nil
	}
	return w
}

// GeoTIFF returns the fully resolved GeoTIFF projection metadata assembled
// from the three LASF_Projection VLRs: key directory (recID 34735), double
// params (recID 34736), and ASCII params (recID 34737). Both VLRs and EVLRs
// are scanned. Returns (nil, nil) if no GeoTIFF directory VLR is found.
func (r *Reader) GeoTIFF() (*GeoTIFFMetadata, error) {
	var dirData, dblData, ascData []byte

	collect := func(userID string, recID uint16, data []byte) {
		if userID != "LASF_Projection" {
			return
		}
		switch recID {
		case 34735:
			dirData = data
		case 34736:
			dblData = data
		case 34737:
			ascData = data
		}
	}

	for _, v := range r.vlrs {
		collect(v.UserID, v.RecordID, v.Data)
	}
	evlrs, _ := r.EVLRs()
	for _, e := range evlrs {
		collect(e.UserID, e.RecordID, e.Data)
	}

	if len(dirData) == 0 {
		return nil, nil
	}
	return ParseGeoTIFF(dirData, dblData, ascData)
}

// CRS returns a string identifying the coordinate reference system, or "" if
// none can be determined. WKT CoordinateSystem records (recID 2112) take
// precedence. Otherwise GeoTIFF keys are interpreted as an EPSG horizontal CRS,
// an EPSG horizontal+vertical compound CRS, or a WKT string synthesized from
// user-defined GeoTIFF keys.
func (r *Reader) CRS() string {
	if wkt := r.WKT(); wkt != nil && wkt.CoordinateSystem != "" {
		return wkt.CoordinateSystem
	}

	geo, err := r.GeoTIFF()
	if geo == nil || err != nil {
		return ""
	}
	return geo.CRS()
}

// VLRs returns all Variable Length Records parsed at Open time.
// The slice is owned by the Reader; do not modify.
func (r *Reader) VLRs() []VLR { return r.vlrs }

// EVLRs returns Extended Variable Length Records.
// Parsed lazily on first call; subsequent calls return the cached slice.
// Returns nil, nil for LAS < 1.4 (the format has no EVLR section) and for
// LAS 1.4 files that declare zero EVLRs. Only returns a non-nil error when
// the EVLR section exists but cannot be read from disk.
func (r *Reader) EVLRs() ([]EVLR, error) {
	if r.evlrsLoaded {
		return r.evlrs, nil
	}
	evlrOffsetPtr := r.header.EVLROffset()
	if evlrOffsetPtr == nil {
		// LAS < 1.4: no EVLR section exists; not an error.
		r.evlrsLoaded = true
		return nil, nil
	}
	evlrOffset := *evlrOffsetPtr
	evlrCount := *r.header.EVLRCount()
	if evlrCount == 0 || evlrOffset == 0 {
		r.evlrsLoaded = true
		return nil, nil
	}
	// Save current stream position so we can restore it after loading EVLRs.
	savePos, err := r.rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("tell current position for EVLR load: %w", err)
	}
	// Adjust for the bufio read-ahead: the logical position is
	// (kernel pos) - (buffered bytes), already handled by ByteStreamInReader.Tell.
	// We need the actual seek position, so we use r.rs directly (not r.stream).
	evlrs, err := readAllEVLRs(r.rs, evlrOffset, evlrCount, savePos)
	if err != nil {
		return nil, err
	}
	// Reinitialise the bufio wrapper after the seek-around.
	r.stream = laz.NewByteStreamInReader(r.rs)
	r.evlrs = evlrs
	r.evlrsLoaded = true
	return r.evlrs, nil
}

// NumPoints returns the total number of points as declared in the header.
func (r *Reader) NumPoints() uint64 { return r.totalPoints }

// Tell returns the index of the next point to be returned by Scan/Next.
func (r *Reader) Tell() uint64 { return r.pointCount }

// ExtraByteDescriptors returns the parsed extra-byte descriptors from the
// LASF_Spec VLR, or nil if no descriptor VLR was present.
func (r *Reader) ExtraByteDescriptors() []ExtraByteDescriptor { return r.extraByteDescs }

// ---------------------------------------------------------------------------
// Point reading
// ---------------------------------------------------------------------------

// Scan decodes the next point into p. Returns io.EOF when all points have
// been read. The point is valid until the next Scan or Next call.
// Zero allocations on the hot path.
func (r *Reader) Scan(p *Point) error {
	if r.pointCount >= r.totalPoints {
		return io.EOF
	}
	if err := r.lp.Read(r.ptBuf); err != nil {
		return fmt.Errorf("read point %d: %w", r.pointCount, err)
	}
	r.pointCount++

	populatePoint(
		p,
		r.header.PointDataFormat,
		r.present,
		r.flatBuf,
		r.onDiskBuf,
		r.items,
		r.offsets,
		r.scaleX, r.scaleY, r.scaleZ,
		r.offsetX, r.offsetY, r.offsetZ,
	)
	return nil
}

// Next decodes the next point and returns it as a newly allocated Point.
// The returned Point is safe to retain across subsequent Scan/Next calls.
// Returns nil, io.EOF when all points have been read.
func (r *Reader) Next() (*Point, error) {
	p := &Point{}
	if err := r.Scan(p); err != nil {
		return nil, err
	}
	// Next must return a Point whose Raw() slice is independent of the shared
	// buffer, so we copy it.
	rawCopy := make([]byte, len(p.raw))
	copy(rawCopy, p.raw)
	p.raw = rawCopy
	// Same for extraBuf.
	if len(p.extraBuf) > 0 {
		ebCopy := make([]byte, len(p.extraBuf))
		copy(ebCopy, p.extraBuf)
		p.extraBuf = ebCopy
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// Seek moves the read position to point index n (0-based).
// The next Scan/Next call will return point n.
// Both forward and backward seeking are supported for both compressed and
// uncompressed files.
//
// Seek returns an error for files with more than 4 294 967 295 points: the
// underlying decompressor passes both the current position and the target to
// LASreadPoint.Seek as uint32, so either value overflowing would corrupt the
// chunk-table navigation.
func (r *Reader) Seek(n uint64) error {
	if n > r.totalPoints {
		return fmt.Errorf("seek to %d: file has only %d points", n, r.totalPoints)
	}
	const maxUint32 = 1<<32 - 1
	if n > maxUint32 || r.pointCount > maxUint32 {
		return fmt.Errorf("seek to %d: point index exceeds engine limit (2^32-1)", n)
	}
	if err := r.lp.Seek(uint32(r.pointCount), uint32(n)); err != nil {
		return fmt.Errorf("seek to point %d: %w", n, err)
	}
	r.pointCount = n
	return nil
}

// Reset moves the read position back to the first point. Equivalent to Seek(0).
func (r *Reader) Reset() error { return r.Seek(0) }

// ---------------------------------------------------------------------------
// ExtraByte convenience method on Reader
// ---------------------------------------------------------------------------

// ExtraByte returns the typed value of a named extra byte field for the given point.
// Requires an ExtraByteDescriptor VLR to have been present in the file.
// Returns ErrNoExtraByteDescriptor if none was found, or ErrUnknownExtraByteField
// if name does not match any descriptor. The returned value is typed according
// to ExtraByteDescriptor.DataType.
func (r *Reader) ExtraByte(p *Point, name string) (any, error) {
	return p.extraByte(name, r.extraByteIndex, r.extraByteDescs)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

// Close releases all resources held by the Reader.
// If the Reader was created with Open (filename), the underlying file is closed.
// If created with OpenReader, the caller's reader is NOT closed.
func (r *Reader) Close() error {
	r.lp.Done()
	if r.ownFile != nil {
		err := r.ownFile.Close()
		r.ownFile = nil
		return err
	}
	return nil
}
