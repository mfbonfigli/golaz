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
	"encoding/binary"
	"fmt"
	"os"
	"testing"
)

type ConfigTestCase struct {
	ID         int
	PointType  uint8
	PointSize  uint16
	Compressor uint16
	CppOk      bool
	NumItems   uint16
	Items      []LASitem
	CppIsStd   bool
	CppPt      uint8
	CppRl      uint16
}

type VLRTestCase struct {
	ID         int
	PointType  uint8
	PointSize  uint16
	Compressor uint16
	Data       []byte
}

func TestLASzipConfigRoundtrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/laszip_config.bin")
	if err != nil {
		t.Fatalf("cannot read config vectors: %v", err)
	}

	if len(raw) < 4 || string(raw[:4]) != "LZCF" {
		t.Fatalf("bad magic: got %q, want LZCF", string(raw[:4]))
	}
	off := 4

	// Read explicit config block count written by the updated C++ generator
	if off+4 > len(raw) {
		t.Fatalf("truncated file reading config spec total count")
	}
	numConfigs := binary.LittleEndian.Uint32(raw[off : off+4])
	off += 4

	var configTests []ConfigTestCase

	// Phase 1: Pure data decoding using bounded execution count
	for i := 0; i < int(numConfigs); i++ {
		if off+6 > len(raw) {
			t.Fatalf("tc %d: truncated header block", i+1)
		}

		tcItem := ConfigTestCase{ID: i + 1}
		tcItem.PointType = raw[off]
		tcItem.PointSize = binary.LittleEndian.Uint16(raw[off+1 : off+3])
		tcItem.Compressor = binary.LittleEndian.Uint16(raw[off+3 : off+5])
		okFlag := raw[off+5]
		tcItem.CppOk = (okFlag == 1)
		off += 6

		if tcItem.CppOk {
			if off+2 > len(raw) {
				t.Fatalf("tc %d: truncated items count block", tcItem.ID)
			}
			tcItem.NumItems = binary.LittleEndian.Uint16(raw[off : off+2])
			off += 2

			tcItem.Items = make([]LASitem, tcItem.NumItems)
			for j := uint16(0); j < tcItem.NumItems; j++ {
				if off+6 > len(raw) {
					t.Fatalf("tc %d: truncated item index %d", tcItem.ID, j)
				}
				tcItem.Items[j].Type = binary.LittleEndian.Uint16(raw[off : off+2])
				tcItem.Items[j].Size = binary.LittleEndian.Uint16(raw[off+2 : off+4])
				tcItem.Items[j].Version = binary.LittleEndian.Uint16(raw[off+4 : off+6])
				off += 6
			}

			if off+1 > len(raw) {
				t.Fatalf("tc %d: truncated standard flags flag", tcItem.ID)
			}
			isStdFlag := raw[off]
			off++
			tcItem.CppIsStd = (isStdFlag == 1)

			if tcItem.CppIsStd {
				if off+3 > len(raw) {
					t.Fatalf("tc %d: truncated explicit mapping metrics block", tcItem.ID)
				}
				tcItem.CppPt = raw[off]
				tcItem.CppRl = binary.LittleEndian.Uint16(raw[off+1 : off+3])
				off += 3
			}
		} else {
			// Bounded error branch mirroring the C++ config generation block
			if off+2 > len(raw) {
				t.Fatalf("tc %d: truncated error length field", tcItem.ID)
			}
			errLen := binary.LittleEndian.Uint16(raw[off : off+2])
			off += 2

			if off+int(errLen) > len(raw) {
				t.Fatalf("tc %d: truncated error message string", tcItem.ID)
			}
			off += int(errLen)
		}
		configTests = append(configTests, tcItem)
	}

	// Phase 2: Decoupled VLR Decoding Section
	var vlrTests []VLRTestCase
	if off+4 <= len(raw) {
		numVLR := binary.LittleEndian.Uint32(raw[off : off+4])
		off += 4
		for i := range numVLR {
			if off+9 > len(raw) {
				t.Fatalf("VLR %d: truncated elements header", i)
			}
			vlrItem := VLRTestCase{ID: int(i)}
			vlrItem.PointType = raw[off]
			vlrItem.PointSize = binary.LittleEndian.Uint16(raw[off+1 : off+3])
			vlrItem.Compressor = binary.LittleEndian.Uint16(raw[off+3 : off+5])
			num := binary.LittleEndian.Uint32(raw[off+5 : off+9])
			off += 9

			if int(num) > len(raw)-off {
				t.Fatalf("VLR %d: payload bounds overflow", i)
			}
			vlrItem.Data = raw[off : off+int(num)]
			off += int(num)
			vlrTests = append(vlrTests, vlrItem)
		}
	}

	// Phase 3: Execute Assertions across clean arrays
	// isUpstreamPF10Quirk reports whether items form a genuine point-format-10
	// set (POINT14 + RGBNIR14 + WAVEPACKET14 [+ BYTE*]) — the one combination
	// where upstream C++ is_standard returns false due to an indexing typo and
	// golaz deliberately diverges.
	isUpstreamPF10Quirk := func(items []LASitem) bool {
		if len(items) != 3 && len(items) != 4 {
			return false
		}
		if !items[0].IsType(LASITEM_POINT14) || !items[1].IsType(LASITEM_RGBNIR14) || !items[2].IsType(LASITEM_WAVEPACKET14) {
			return false
		}
		return len(items) == 3 || items[3].IsType(LASITEM_BYTE) || items[3].IsType(LASITEM_BYTE14)
	}

	for _, tcItem := range configTests {
		t.Run(fmt.Sprintf("tc%d_pt%d_sz%d_c%d", tcItem.ID, tcItem.PointType, tcItem.PointSize, tcItem.Compressor), func(t *testing.T) {
			goItems, goRecordLen, goErr := Setup(tcItem.PointType, tcItem.PointSize, tcItem.Compressor)
			if tcItem.CppOk != (goErr == nil) {
				t.Fatalf("ok mismatch: C++=%v Go=%v (err=%v)", tcItem.CppOk, goErr == nil, goErr)
			}
			if !tcItem.CppOk {
				return
			}

			if len(goItems) != int(tcItem.NumItems) {
				t.Errorf("numItems mismatch: C++=%d Go=%d", tcItem.NumItems, len(goItems))
			} else {
				for j := 0; j < int(tcItem.NumItems); j++ {
					if goItems[j].Type != tcItem.Items[j].Type ||
						goItems[j].Size != tcItem.Items[j].Size ||
						goItems[j].Version != tcItem.Items[j].Version {
						t.Errorf("item[%d] mismatch: C++={T:%d S:%d V:%d} Go={T:%d S:%d V:%d}",
							j, tcItem.Items[j].Type, tcItem.Items[j].Size, tcItem.Items[j].Version,
							goItems[j].Type, goItems[j].Size, goItems[j].Version)
					}
				}
			}

			var goPt uint8
			var goRl uint16

			lzTmp := LASzip{
				NumItems: uint16(len(goItems)),
				Items:    goItems,
			}

			goIsStd := lzTmp.IsStandard(&goPt, &goRl)
			if tcItem.CppIsStd != goIsStd {
				if !tcItem.CppIsStd && goIsStd && isUpstreamPF10Quirk(goItems) {
					// Known upstream bug: C++ is_standard tests items[1] for
					// WAVEPACKET14 where items[2] is meant, so the golden data
					// records genuine pf10 item sets as non-standard. golaz
					// deliberately applies the intended check (see IsStandard).
					if goPt != 10 {
						t.Errorf("pf10 carve-out: pointType = %d, want 10", goPt)
					}
					if goRl != goRecordLen {
						t.Errorf("pf10 carve-out: recordLength from Setup=%d != isStandardItems=%d", goRecordLen, goRl)
					}
					return
				}
				t.Errorf("is_standard mismatch: C++=%v Go=%v", tcItem.CppIsStd, goIsStd)
			}

			if tcItem.CppIsStd {
				if goPt != tcItem.CppPt {
					t.Errorf("pointType mismatch: C++=%d Go=%d", tcItem.CppPt, goPt)
				}
				if goRl != tcItem.CppRl {
					t.Errorf("recordLength mismatch: C++=%d Go=%d", tcItem.CppRl, goRl)
				}
				if goRl != goRecordLen {
					t.Errorf("recordLength from Setup=%d != isStandardItems=%d", goRecordLen, goRl)
				}
			}
		})
	}

	if len(vlrTests) > 0 {
		t.Run("VLR", func(t *testing.T) {
			for _, vlrItem := range vlrTests {
				var lz LASzip
				err := lz.Unpack(vlrItem.Data)
				if err != nil {
					t.Errorf("VLR %d (pt=%d sz=%d comp=%d): Go Unpack error: %v", vlrItem.ID, vlrItem.PointType, vlrItem.PointSize, vlrItem.Compressor, err)
					continue
				}
				goItems, _, err := Setup(vlrItem.PointType, vlrItem.PointSize, vlrItem.Compressor)
				if err != nil {
					t.Errorf("VLR %d: Go Setup error: %v", vlrItem.ID, err)
					continue
				}
				if len(goItems) != int(lz.NumItems) {
					t.Errorf("VLR %d: numItems mismatch: unpacked=%d setup=%d", vlrItem.ID, lz.NumItems, len(goItems))
					continue
				}
				for j := range goItems {
					if lz.Items[j].Type != goItems[j].Type ||
						lz.Items[j].Size != goItems[j].Size ||
						lz.Items[j].Version != goItems[j].Version {
						t.Errorf("VLR %d item[%d]: unpacked={T:%d S:%d V:%d} setup={T:%d S:%d V:%d}",
							vlrItem.ID, j, lz.Items[j].Type, lz.Items[j].Size, lz.Items[j].Version,
							goItems[j].Type, goItems[j].Size, goItems[j].Version)
					}
				}
			}
		})
	}
}
