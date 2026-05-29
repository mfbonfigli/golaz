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
	"encoding/csv"
	"os"
	"strconv"
	"strings"
	"testing"
)

// refRecord holds one row from the reference CSV used by reader_test.go.
type refRecord struct {
	X, Y, Z          float64
	Intensity        uint16
	ReturnNumber     uint8
	NumberOfReturns  uint8
	ScanDirection    uint8
	EOFL             uint8
	Classification   uint8
	UserData         uint8
	PointSourceID    uint16
	ScanAngle        int16
	GPSTime          float64
	Red, Green, Blue uint16
	NIR              uint16
	WaveIdx          uint8
	WaveOff          uint64
	WaveSize         uint32
	WaveLoc          float32
	XT, YT, ZT       float64
	GridID           uint32
	Confidence       float32
}

// parseRefCSV reads a 26-column reference CSV produced by the C++ reference decoder.
func parseRefCSV(t *testing.T, path string) []refRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ref csv %q: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read ref csv %q: %v", path, err)
	}
	if len(rows) < 2 {
		t.Fatalf("ref csv %q: no data rows", path)
	}

	recs := make([]refRecord, len(rows)-1)
	for i := 1; i < len(rows); i++ {
		cols := rows[i]
		if len(cols) < 26 {
			t.Fatalf("ref csv %q row %d: need 26 cols, got %d", path, i, len(cols))
		}
		rr := &recs[i-1]
		rr.X = pFloat(cols[0])
		rr.Y = pFloat(cols[1])
		rr.Z = pFloat(cols[2])
		rr.Intensity = uint16(pInt(cols[3]))
		rr.ReturnNumber = uint8(pInt(cols[4]))
		rr.NumberOfReturns = uint8(pInt(cols[5]))
		rr.ScanDirection = uint8(pInt(cols[6]))
		rr.EOFL = uint8(pInt(cols[7]))
		rr.Classification = uint8(pInt(cols[8]))
		rr.UserData = uint8(pInt(cols[9]))
		rr.PointSourceID = uint16(pInt(cols[10]))
		rr.ScanAngle = int16(pInt(cols[11]))
		rr.GPSTime = pFloat(cols[12])
		rr.Red = uint16(pInt(cols[13]))
		rr.Green = uint16(pInt(cols[14]))
		rr.Blue = uint16(pInt(cols[15]))
		rr.NIR = uint16(pInt(cols[16]))
		rr.WaveIdx = uint8(pInt(cols[17]))
		rr.WaveOff = uint64(pInt(cols[18]))
		rr.WaveSize = uint32(pInt(cols[19]))
		rr.WaveLoc = float32(pFloat(cols[20]))
		rr.XT = pFloat(cols[21])
		rr.YT = pFloat(cols[22])
		rr.ZT = pFloat(cols[23])
		rr.GridID = uint32(pInt(cols[24]))
		rr.Confidence = float32(pFloat(cols[25]))
	}
	return recs
}

func pFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func pInt(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}
