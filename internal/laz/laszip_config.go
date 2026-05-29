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
// laszip_config.go — LASzip configuration, VLR unpacking, and point type
// decomposition ported from src/laszip.hpp/cpp. Read-relevant subset only.
package laz

import (
	"encoding/binary"
	"fmt"
)

// Compressor types (from laszip.hpp)
const (
	LASZIP_COMPRESSOR_NONE              = 0
	LASZIP_COMPRESSOR_POINTWISE         = 1
	LASZIP_COMPRESSOR_POINTWISE_CHUNKED = 2
	LASZIP_COMPRESSOR_LAYERED_CHUNKED   = 3
)

const LASZIP_CODER_ARITHMETIC = 0
const LASZIP_CHUNK_SIZE_DEFAULT = 50000

// LAS structures types
const (
	LASITEM_BYTE         = 0
	LASITEM_SHORT        = 1
	LASITEM_INT          = 2
	LASITEM_LONG         = 3
	LASITEM_FLOAT        = 4
	LASITEM_DOUBLE       = 5
	LASITEM_POINT10      = 6
	LASITEM_GPSTIME11    = 7
	LASITEM_RGB12        = 8
	LASITEM_WAVEPACKET13 = 9
	LASITEM_POINT14      = 10
	LASITEM_RGB14        = 11
	LASITEM_RGBNIR14     = 12
	LASITEM_WAVEPACKET14 = 13
	LASITEM_BYTE14       = 14
)

type LASitem struct {
	Type    uint16
	Size    uint16
	Version uint16
}

func (item *LASitem) IsType(t uint16) bool {
	return item.Type == t
}

type LASzip struct {
	Compressor           uint16
	Coder                uint16
	VersionMajor         uint8
	VersionMinor         uint8
	VersionRevision      uint16
	Options              uint32
	ChunkSize            uint32
	NumberOfSpecialEVLRs int64
	OffsetToSpecialEVLRs int64
	NumItems             uint16
	Items                []LASitem
}

func NewLASzip() *LASzip {
	return &LASzip{
		Compressor:           LASZIP_COMPRESSOR_POINTWISE_CHUNKED,
		Coder:                LASZIP_CODER_ARITHMETIC,
		VersionMajor:         3,
		VersionMinor:         5,
		VersionRevision:      0,
		ChunkSize:            LASZIP_CHUNK_SIZE_DEFAULT,
		NumberOfSpecialEVLRs: -1,
		OffsetToSpecialEVLRs: -1,
	}
}

func checkItem(item *LASitem) error {
	switch item.Type {
	case LASITEM_POINT10:
		if item.Size != 20 {
			return fmt.Errorf("POINT10 size=%d", item.Size)
		}
		if item.Version > 2 {
			return fmt.Errorf("POINT10 version=%d (>2)", item.Version)
		}
	case LASITEM_GPSTIME11:
		if item.Size != 8 {
			return fmt.Errorf("GPSTIME11 size=%d", item.Size)
		}
		if item.Version > 2 {
			return fmt.Errorf("GPSTIME11 version=%d (>2)", item.Version)
		}
	case LASITEM_RGB12:
		if item.Size != 6 {
			return fmt.Errorf("RGB12 size=%d", item.Size)
		}
		if item.Version > 2 {
			return fmt.Errorf("RGB12 version=%d (>2)", item.Version)
		}
	case LASITEM_BYTE:
		if item.Size < 1 {
			return fmt.Errorf("BYTE size=%d", item.Size)
		}
		if item.Version > 2 {
			return fmt.Errorf("BYTE version=%d (>2)", item.Version)
		}
	case LASITEM_POINT14:
		if item.Size != 30 {
			return fmt.Errorf("POINT14 size=%d", item.Size)
		}
		if item.Version != 0 && item.Version != 2 && item.Version != 3 && item.Version != 4 {
			return fmt.Errorf("POINT14 version=%d (not 0/2/3/4)", item.Version)
		}
	case LASITEM_RGB14:
		if item.Size != 6 {
			return fmt.Errorf("RGB14 size=%d", item.Size)
		}
		if item.Version != 0 && item.Version != 2 && item.Version != 3 && item.Version != 4 {
			return fmt.Errorf("RGB14 version=%d (not 0/2/3/4)", item.Version)
		}
	case LASITEM_RGBNIR14:
		if item.Size != 8 {
			return fmt.Errorf("RGBNIR14 size=%d", item.Size)
		}
		if item.Version != 0 && item.Version != 2 && item.Version != 3 && item.Version != 4 {
			return fmt.Errorf("RGBNIR14 version=%d (not 0/2/3/4)", item.Version)
		}
	case LASITEM_BYTE14:
		if item.Size < 1 {
			return fmt.Errorf("BYTE14 size=%d", item.Size)
		}
		if item.Version != 0 && item.Version != 2 && item.Version != 3 && item.Version != 4 {
			return fmt.Errorf("BYTE14 version=%d (not 0/2/3/4)", item.Version)
		}
	case LASITEM_WAVEPACKET13:
		if item.Size != 29 {
			return fmt.Errorf("WAVEPACKET13 size=%d", item.Size)
		}
		if item.Version > 1 {
			return fmt.Errorf("WAVEPACKET13 version=%d (>1)", item.Version)
		}
	case LASITEM_WAVEPACKET14:
		if item.Size != 29 {
			return fmt.Errorf("WAVEPACKET14 size=%d", item.Size)
		}
		if item.Version != 0 && item.Version != 3 && item.Version != 4 {
			return fmt.Errorf("WAVEPACKET14 version=%d (not 0/3/4)", item.Version)
		}
	default:
		return fmt.Errorf("unknown item type %d", item.Type)
	}
	return nil
}

func (lz *LASzip) Unpack(data []byte) error {
	if len(data) < 34 {
		return fmt.Errorf("too few bytes (%d < 34)", len(data))
	}
	if (len(data)-34)%6 != 0 {
		return fmt.Errorf("wrong byte count")
	}
	expectedNumItems := (len(data) - 34) / 6

	lz.Compressor = binary.LittleEndian.Uint16(data[0:2])
	lz.Coder = binary.LittleEndian.Uint16(data[2:4])
	lz.VersionMajor = data[4]
	lz.VersionMinor = data[5]
	lz.VersionRevision = binary.LittleEndian.Uint16(data[6:8])
	lz.Options = binary.LittleEndian.Uint32(data[8:12])
	lz.ChunkSize = binary.LittleEndian.Uint32(data[12:16])
	lz.NumberOfSpecialEVLRs = int64(binary.LittleEndian.Uint64(data[16:24]))
	lz.OffsetToSpecialEVLRs = int64(binary.LittleEndian.Uint64(data[24:32]))
	lz.NumItems = binary.LittleEndian.Uint16(data[32:34])

	if int(lz.NumItems) != expectedNumItems {
		return fmt.Errorf("num_items mismatch: %d vs %d", lz.NumItems, expectedNumItems)
	}

	lz.Items = make([]LASitem, lz.NumItems)
	b := data[34:]

	for i := 0; i < int(lz.NumItems); i++ {
		lz.Items[i].Type = binary.LittleEndian.Uint16(b[0:2])
		lz.Items[i].Size = binary.LittleEndian.Uint16(b[2:4])
		lz.Items[i].Version = binary.LittleEndian.Uint16(b[4:6])
		b = b[6:]

		if err := checkItem(&lz.Items[i]); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}
	return nil
}

func GetDefaultVersion(pointType, lasMajor, lasMinor uint8) uint16 {
	if pointType >= 6 && pointType <= 10 {
		if lasMajor >= 1 && lasMinor >= 5 {
			return 4
		}
		return 3
	}
	return 2
}

func (lz *LASzip) IsStandard(pointType *uint8, recordLength *uint16) bool {
	if lz.NumItems < 1 || lz.NumItems > 5 || int(lz.NumItems) > len(lz.Items) {
		return false
	}

	var total uint16
	for i := 0; i < int(lz.NumItems); i++ {
		total += lz.Items[i].Size
	}
	if recordLength != nil {
		*recordLength = total
	}

	items := lz.Items
	if items[0].IsType(LASITEM_POINT10) && items[0].Size == 20 {
		if lz.NumItems == 1 {
			if pointType != nil {
				*pointType = 0
			}
			return true
		}
		if items[1].IsType(LASITEM_GPSTIME11) && items[1].Size == 8 {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 1
				}
				return true
			}
			if items[2].IsType(LASITEM_RGB12) && items[2].Size == 6 {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 3
					}
					return true
				}
				if items[3].IsType(LASITEM_WAVEPACKET13) && items[3].Size == 29 {
					if lz.NumItems == 4 {
						if pointType != nil {
							*pointType = 5
						}
						return true
					}
					if items[4].IsType(LASITEM_BYTE) {
						if lz.NumItems == 5 {
							if pointType != nil {
								*pointType = 5
							}
							return true
						}
					}
				} else if items[3].IsType(LASITEM_BYTE) {
					if lz.NumItems == 4 {
						if pointType != nil {
							*pointType = 3
						}
						return true
					}
				}
			} else if items[2].IsType(LASITEM_WAVEPACKET13) && items[2].Size == 29 {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 4
					}
					return true
				}
				if items[3].IsType(LASITEM_BYTE) {
					if lz.NumItems == 4 {
						if pointType != nil {
							*pointType = 4
						}
						return true
					}
				}
			} else if items[2].IsType(LASITEM_BYTE) {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 1
					}
					return true
				}
			}
		} else if items[1].IsType(LASITEM_RGB12) && items[1].Size == 6 {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 2
				}
				return true
			}
			if items[2].IsType(LASITEM_BYTE) {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 2
					}
					return true
				}
			}
		} else if items[1].IsType(LASITEM_BYTE) {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 0
				}
				return true
			}
		}
	} else if items[0].IsType(LASITEM_POINT14) && items[0].Size == 30 {
		if lz.NumItems == 1 {
			if pointType != nil {
				*pointType = 6
			}
			return true
		}
		if items[1].IsType(LASITEM_RGB14) && items[1].Size == 6 {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 7
				}
				return true
			}
			if items[2].IsType(LASITEM_BYTE) || items[2].IsType(LASITEM_BYTE14) {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 7
					}
					return true
				}
			}
		} else if items[1].IsType(LASITEM_RGBNIR14) && items[1].Size == 8 {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 8
				}
				return true
			}
			if items[2].IsType(LASITEM_BYTE) || items[2].IsType(LASITEM_BYTE14) {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 8
					}
					return true
				}
			}
			// Point format 10 contains WAVEPACKET14 here, but it is explicitly omitted
			// from native C++ laszip::is_standard() mapping matrices.
		} else if (items[1].IsType(LASITEM_WAVEPACKET13) || items[1].IsType(LASITEM_WAVEPACKET14)) && items[1].Size == 29 {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 9
				}
				return true
			}
			if items[2].IsType(LASITEM_BYTE) || items[2].IsType(LASITEM_BYTE14) {
				if lz.NumItems == 3 {
					if pointType != nil {
						*pointType = 9
					}
					return true
				}
			}
		} else if items[1].IsType(LASITEM_BYTE) || items[1].IsType(LASITEM_BYTE14) {
			if lz.NumItems == 2 {
				if pointType != nil {
					*pointType = 6
				}
				return true
			}
		}
	}
	return false
}

func Setup(pointType uint8, pointSize uint16, compressor uint16) (items []LASitem, recordLength uint16, err error) {
	if pointType == 255 && compressor == 65535 {
		return []LASitem{}, 0, nil
	}

	if compressor >= 4 && compressor != 65535 {
		return nil, 0, fmt.Errorf("compressor %d not supported", compressor)
	}

	if pointType <= 5 {
		if compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED {
			return nil, 0, fmt.Errorf("invalid compressor %d for point type %d", compressor, pointType)
		}
	} else if pointType <= 10 {
		if compressor == LASZIP_COMPRESSOR_POINTWISE || compressor == LASZIP_COMPRESSOR_POINTWISE_CHUNKED {
			return nil, 0, fmt.Errorf("invalid compressor %d for point type %d", compressor, pointType)
		}
	}

	items, recordLength, err = setupItems(pointType, pointSize)
	if err != nil {
		return nil, 0, err
	}
	if compressor != 0 && compressor != 65535 {
		if items[0].Type == LASITEM_POINT14 {
			if compressor != LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				return nil, 0, fmt.Errorf("POINT14 requires LAYERED_CHUNKED compressor, got %d", compressor)
			}
		}
		for i := range items {
			items[i].Version = 2
			if items[i].Type == LASITEM_WAVEPACKET13 {
				items[i].Version = 1
			} else if items[i].Type >= LASITEM_POINT14 {
				items[i].Version = 3
			}
		}
	}
	return items, recordLength, nil
}

// checkItems validates all items and, if pointSize > 0, verifies
// that the sum of item sizes matches.
func (lz *LASzip) checkItems(pointSize uint16) error {
	if lz.NumItems == 0 {
		return fmt.Errorf("number of items cannot be zero")
	}
	var total uint16
	for i := uint16(0); i < lz.NumItems; i++ {
		if err := checkItem(&lz.Items[i]); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		total += lz.Items[i].Size
	}
	if pointSize != 0 && pointSize != total {
		return fmt.Errorf("point has size of %d but items only add up to %d bytes", pointSize, total)
	}
	return nil
}

// check validates compressor, coder, and all items.
func (lz *LASzip) check(pointSize uint16) error {
	if lz.Compressor >= 4 {
		return fmt.Errorf("compressor %d not supported", lz.Compressor)
	}
	if lz.Coder != LASZIP_CODER_ARITHMETIC {
		return fmt.Errorf("coder %d not supported", lz.Coder)
	}
	return lz.checkItems(pointSize)
}

func setupItems(pointType uint8, pointSize uint16) (items []LASitem, recordLength uint16, err error) {
	var havePoint14, haveGpsTime, haveRgb, haveNir, haveWave bool
	var baseSize uint16

	switch pointType {
	case 0:
		baseSize = 20
	case 1:
		baseSize = 28
		haveGpsTime = true
	case 2:
		baseSize = 26
		haveRgb = true
	case 3:
		baseSize = 34
		haveGpsTime = true
		haveRgb = true
	case 4:
		baseSize = 57
		haveGpsTime = true
		haveWave = true
	case 5:
		baseSize = 63
		haveGpsTime = true
		haveRgb = true
		haveWave = true
	case 6:
		baseSize = 30
		havePoint14 = true
	case 7:
		baseSize = 36
		havePoint14 = true
		haveRgb = true
	case 8:
		baseSize = 38
		havePoint14 = true
		haveRgb = true
		haveNir = true
	case 9:
		baseSize = 59
		havePoint14 = true
		haveWave = true
	case 10:
		baseSize = 67
		havePoint14 = true
		haveRgb = true
		haveNir = true
		haveWave = true
	default:
		return nil, 0, fmt.Errorf("unsupported point type %d", pointType)
	}

	if pointSize == 0 || pointSize < baseSize || pointSize > 1000 {
		return nil, 0, fmt.Errorf("invalid pointSize %d for type %d", pointSize, pointType)
	}

	extraBytes := int(pointSize) - int(baseSize)

	addItem := func(typ uint16, size uint16) {
		items = append(items, LASitem{Type: typ, Size: size, Version: 0})
		recordLength += size
	}

	if havePoint14 {
		addItem(LASITEM_POINT14, 30)
	} else {
		addItem(LASITEM_POINT10, 20)
	}
	if haveGpsTime {
		addItem(LASITEM_GPSTIME11, 8)
	}
	if haveRgb {
		if havePoint14 {
			if haveNir {
				addItem(LASITEM_RGBNIR14, 8)
			} else {
				addItem(LASITEM_RGB14, 6)
			}
		} else {
			addItem(LASITEM_RGB12, 6)
		}
	}
	if haveWave {
		if havePoint14 {
			addItem(LASITEM_WAVEPACKET14, 29)
		} else {
			addItem(LASITEM_WAVEPACKET13, 29)
		}
	}
	if extraBytes > 0 {
		if havePoint14 {
			addItem(LASITEM_BYTE14, uint16(extraBytes))
		} else {
			addItem(LASITEM_BYTE, uint16(extraBytes))
		}
	}

	return items, recordLength, nil
}

// ---------------------------------------------------------------------------
// Method-based setup helpers (C++ overload equivalents)
//
//   SetupByPointType(pointType, pointSize, compressor) — C++: setup(U8, U16, U16)
//   SetupFromItems(numItems, items, compressor)        — C++: setup(U16, items*, U16)
//
// Both validate input, store items on LASzip, set Compressor/ChunkSize,
// and call requestVersion internally for the per-item compression version.
// ---------------------------------------------------------------------------

// setupItemsCore decomposes point type + size into items, respecting
// Options bit 0 (compatibility mode) when set.
func (lz *LASzip) setupItemsCore(pointType uint8, pointSize uint16) ([]LASitem, uint16, error) {
	var havePoint14, haveGpsTime, haveRgb, haveNir, haveWave bool
	var baseSize uint16

	switch pointType {
	case 0:
		baseSize = 20
	case 1:
		baseSize = 28
		haveGpsTime = true
	case 2:
		baseSize = 26
		haveRgb = true
	case 3:
		baseSize = 34
		haveGpsTime = true
		haveRgb = true
	case 4:
		baseSize = 57
		haveGpsTime = true
		haveWave = true
	case 5:
		baseSize = 63
		haveGpsTime = true
		haveRgb = true
		haveWave = true
	case 6:
		baseSize = 30
		havePoint14 = true
	case 7:
		baseSize = 36
		havePoint14 = true
		haveRgb = true
	case 8:
		baseSize = 38
		havePoint14 = true
		haveRgb = true
		haveNir = true
	case 9:
		baseSize = 59
		havePoint14 = true
		haveWave = true
	case 10:
		baseSize = 67
		havePoint14 = true
		haveRgb = true
		haveNir = true
		haveWave = true
	default:
		return nil, 0, fmt.Errorf("unsupported point type %d", pointType)
	}

	extraBytes := max(int(pointSize)-int(baseSize), 0)

	// LAS 1.4 compatibility mode: remap POINT14→POINT10, add 5 extra bytes,
	// always include GPSTIME11, fold NIR into extra bytes.
	if havePoint14 && (lz.Options&1) != 0 {
		extraBytes += 5
		haveGpsTime = true
		havePoint14 = false
		if haveNir {
			extraBytes += 2
			haveNir = false
		}
	}

	var items []LASitem
	var recordLength uint16

	addItem := func(typ uint16, size uint16) {
		items = append(items, LASitem{Type: typ, Size: size, Version: 0})
		recordLength += size
	}

	if havePoint14 {
		addItem(LASITEM_POINT14, 30)
	} else {
		addItem(LASITEM_POINT10, 20)
	}
	if haveGpsTime {
		addItem(LASITEM_GPSTIME11, 8)
	}
	if haveRgb {
		if havePoint14 {
			if haveNir {
				addItem(LASITEM_RGBNIR14, 8)
			} else {
				addItem(LASITEM_RGB14, 6)
			}
		} else {
			addItem(LASITEM_RGB12, 6)
		}
	}
	if haveWave {
		if havePoint14 {
			addItem(LASITEM_WAVEPACKET14, 29)
		} else {
			addItem(LASITEM_WAVEPACKET13, 29)
		}
	}
	if extraBytes > 0 {
		if havePoint14 {
			addItem(LASITEM_BYTE14, uint16(extraBytes))
		} else {
			addItem(LASITEM_BYTE, uint16(extraBytes))
		}
	}

	return items, recordLength, nil
}

// SetupByPointType decomposes a point type + size into items, stores them
// on lz, and configures Compressor / ChunkSize / per-item Version.
// C++ original: LASzip::setup(U8 point_type, U16 point_size, U16 compressor)
func (lz *LASzip) SetupByPointType(pointType uint8, pointSize uint16, compressor uint16) error {
	if compressor >= 4 && compressor != 65535 {
		return fmt.Errorf("compressor %d not supported", compressor)
	}
	if pointType <= 5 {
		if compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED {
			return fmt.Errorf("invalid compressor %d for point type %d", compressor, pointType)
		}
	} else if pointType <= 10 {
		if compressor == LASZIP_COMPRESSOR_POINTWISE || compressor == LASZIP_COMPRESSOR_POINTWISE_CHUNKED {
			return fmt.Errorf("invalid compressor %d for point type %d", compressor, pointType)
		}
	}

	items, _, err := lz.setupItemsCore(pointType, pointSize)
	if err != nil {
		return err
	}

	if compressor != 0 && compressor != 65535 {
		if items[0].Type == LASITEM_POINT14 {
			if compressor != LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				return fmt.Errorf("POINT14 requires LAYERED_CHUNKED compressor, got %d", compressor)
			}
			lz.Compressor = LASZIP_COMPRESSOR_LAYERED_CHUNKED
		} else {
			if compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				lz.Compressor = LASZIP_COMPRESSOR_POINTWISE_CHUNKED
			} else {
				lz.Compressor = compressor
			}
		}
		if compressor != LASZIP_COMPRESSOR_POINTWISE {
			if lz.ChunkSize == 0 {
				lz.ChunkSize = LASZIP_CHUNK_SIZE_DEFAULT
			}
		}
	} else {
		lz.Compressor = LASZIP_COMPRESSOR_NONE
	}

	lz.NumItems = uint16(len(items))
	lz.Items = items
	if compressor != 0 && compressor != 65535 {
		lz.RequestVersion(2)
	}
	return nil
}

// SetupFromItems stores a pre-validated item array + compressor on lz.
// C++ original: LASzip::setup(U16 num_items, const LASitem* items, U16 compressor)
func (lz *LASzip) SetupFromItems(numItems uint16, items []LASitem, compressor uint16) error {
	if compressor > LASZIP_COMPRESSOR_LAYERED_CHUNKED {
		return fmt.Errorf("compressor %d not supported", compressor)
	}
	if err := checkItemsStatic(numItems, items, nil); err != nil {
		return err
	}

	if compressor != 0 {
		if items[0].Type == LASITEM_POINT14 {
			if compressor != LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				return fmt.Errorf("POINT14 requires LAYERED_CHUNKED compressor, got %d", compressor)
			}
			lz.Compressor = LASZIP_COMPRESSOR_LAYERED_CHUNKED
		} else {
			if compressor == LASZIP_COMPRESSOR_LAYERED_CHUNKED {
				lz.Compressor = LASZIP_COMPRESSOR_POINTWISE_CHUNKED
			} else {
				lz.Compressor = compressor
			}
		}
		if compressor != LASZIP_COMPRESSOR_POINTWISE {
			if lz.ChunkSize == 0 {
				lz.ChunkSize = LASZIP_CHUNK_SIZE_DEFAULT
			}
		}
	} else {
		lz.Compressor = LASZIP_COMPRESSOR_NONE
	}

	lz.NumItems = numItems
	lz.Items = make([]LASitem, numItems)
	copy(lz.Items, items)
	return nil
}

// checkItemsStatic validates items without a LASzip receiver.
func checkItemsStatic(numItems uint16, items []LASitem, pointSize *uint16) error {
	if numItems == 0 {
		return fmt.Errorf("number of items cannot be zero")
	}
	var total uint16
	for i := range numItems {
		if err := checkItem(&items[i]); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
		total += items[i].Size
	}
	if pointSize != nil && *pointSize != 0 && *pointSize != total {
		return fmt.Errorf("point has size of %d but items only add up to %d bytes", *pointSize, total)
	}
	return nil
}

// SetChunkSize sets the chunk size for the compressor.
// Only valid when compressor is not POINTWISE (unchunked).
func (lz *LASzip) SetChunkSize(chunkSize uint32) error {
	if lz.NumItems == 0 {
		return fmt.Errorf("call setup() before setting chunk size")
	}
	if lz.Compressor != LASZIP_COMPRESSOR_POINTWISE {
		lz.ChunkSize = chunkSize
		return nil
	}
	return fmt.Errorf("cannot set chunk size for pointwise compressor")
}

// RequestVersion sets per-item compression versions according to C++ rules:
//   - Point types 0-5: min(2, requested) — never use v3/v4
//   - WAVEPACKET13: always v1
//   - Point types 6-10: max(3, requested) — never use v1/v2
//
// Called automatically by SetupByPointType(requestedVersion=2).
func (lz *LASzip) RequestVersion(requestedVersion uint16) error {
	if lz.NumItems == 0 {
		return fmt.Errorf("call setup() before requesting version")
	}
	if lz.Compressor == LASZIP_COMPRESSOR_NONE {
		if requestedVersion > 0 {
			return fmt.Errorf("without compression version is always 0")
		}
		return nil
	}
	if requestedVersion < 1 {
		return fmt.Errorf("with compression version is at least 1")
	}
	if requestedVersion > 4 {
		return fmt.Errorf("version larger than 4 not supported")
	}
	for i := uint16(0); i < lz.NumItems; i++ {
		switch lz.Items[i].Type {
		case LASITEM_POINT10, LASITEM_GPSTIME11, LASITEM_RGB12, LASITEM_BYTE:
			// no version 3 or 4
			v := min(requestedVersion, 2)
			lz.Items[i].Version = v
		case LASITEM_WAVEPACKET13:
			// always version 1
			lz.Items[i].Version = 1
		case LASITEM_POINT14, LASITEM_RGB14, LASITEM_RGBNIR14,
			LASITEM_WAVEPACKET14, LASITEM_BYTE14:
			// no version 1 or 2
			v := max(requestedVersion, 3)
			lz.Items[i].Version = v
		default:
			return fmt.Errorf("item type %d not supported in requestVersion", lz.Items[i].Type)
		}
	}
	return nil
}
