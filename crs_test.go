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

// crs_test.go — end-to-end tests for WKT, GeoTIFF, and CRS extraction.
//
// Test data lives in testdata/las/vlr/ and covers:
//   - Pure GeoTIFF with projected CRS (EPSG:26915, 26916, 32617)
//   - Pure GeoTIFF with geographic CRS (EPSG:4326)
//   - File with both WKT and GeoTIFF (test_epsg_4047.las) — WKT wins
//   - GeoTIFF directory with user-defined codes (32767) — CRS() returns ""
//   - Empty GeoTIFF double/ASCII params VLRs
//   - File with 390 VLRs (lots_of_vlr) — stress test VLR parsing
//   - File with no CRS (extrabytes.las) — nil/empty returns

import (
	"encoding/binary"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

const vlrDir = "internal/laz/testdata/las/vlr"

func openVLR(t *testing.T, name string) *Reader {
	t.Helper()
	r, err := Open(filepath.Join(vlrDir, name))
	if err != nil {
		t.Fatalf("Open %q: %v", name, err)
	}
	return r
}

func geoWithShorts(values map[uint16]uint16) *GeoTIFFMetadata {
	geo := &GeoTIFFMetadata{Keys: make(map[uint16]*GeoTIFFKey, len(values))}
	for keyID, value := range values {
		geo.Keys[keyID] = &GeoTIFFKey{
			KeyID:    keyID,
			Type:     GTTagTypeShort,
			Count:    1,
			rawValue: value,
		}
	}
	return geo
}

func packShorts(values ...uint16) []byte {
	out := make([]byte, len(values)*2)
	for i, value := range values {
		binary.LittleEndian.PutUint16(out[i*2:], value)
	}
	return out
}

func packDoubles(values ...float64) []byte {
	out := make([]byte, len(values)*8)
	for i, value := range values {
		binary.LittleEndian.PutUint64(out[i*8:], math.Float64bits(value))
	}
	return out
}

// ---------------------------------------------------------------------------
// WKT tests
// ---------------------------------------------------------------------------

func TestWKT_PresentInFile(t *testing.T) {
	// test_epsg_4047.las has LASF_Projection recID=2112 with WKT.
	r := openVLR(t, "test_epsg_4047.las")
	defer r.Close()

	wkt := r.WKT()
	if wkt == nil {
		t.Fatal("WKT() returned nil; expected WKT data")
	}
	if wkt.CoordinateSystem == "" {
		t.Error("CoordinateSystem is empty")
	}
	// Verify known substring from the WKT string provided by the user.
	if !strings.Contains(wkt.CoordinateSystem, "GRS 1980 Authalic Sphere") {
		t.Errorf("CoordinateSystem does not contain expected content; got: %q", wkt.CoordinateSystem[:min(80, len(wkt.CoordinateSystem))])
	}
	if !strings.Contains(wkt.CoordinateSystem, `AUTHORITY["EPSG","4047"]`) {
		t.Errorf("CoordinateSystem missing EPSG:4047 authority; got prefix: %q", wkt.CoordinateSystem[:min(80, len(wkt.CoordinateSystem))])
	}
}

func TestWKT_AbsentInFile(t *testing.T) {
	// epsg_26915 files have no LASF_Projection recID=2112 (only GeoTIFF).
	r := openVLR(t, "1.2_0_epsg26915.las")
	defer r.Close()
	if w := r.WKT(); w != nil {
		t.Errorf("expected nil WKT for GeoTIFF-only file; got %+v", w)
	}
}

func TestWKT_WrongUserID_Ignored(t *testing.T) {
	// 1.1_0_epsg26915.las has recID=2112 but under userID="liblas", not
	// "LASF_Projection". Must NOT be parsed as WKT.
	r := openVLR(t, "1.1_0_epsg26915.las")
	defer r.Close()
	if w := r.WKT(); w != nil {
		t.Errorf("WKT with non-standard userID should be ignored; got: %+v", w)
	}
}

// ---------------------------------------------------------------------------
// GeoTIFF tests
// ---------------------------------------------------------------------------

func TestGeoTIFF_ProjectedEPSG26915(t *testing.T) {
	// All 1.x_N_epsg26915.las files carry the same GeoTIFF directory:
	// key 3072 (ProjectedCSTypeGeoKey) = 26915.
	for _, name := range []string{
		"1.1_0_epsg26915.las",
		"1.2_0_epsg26915.las",
		"1.2_1_epsg26915.las",
		"1.2_2_epsg26915.las",
		"1.2_3_epsg26915.las",
	} {
		t.Run(name, func(t *testing.T) {
			r := openVLR(t, name)
			defer r.Close()
			geo, err := r.GeoTIFF()
			if err != nil {
				t.Fatalf("GeoTIFF: %v", err)
			}
			if geo == nil {
				t.Fatal("GeoTIFF() returned nil")
			}
			key := geo.Keys[3072]
			if key == nil {
				t.Fatal("ProjectedCSTypeGeoKey (3072) not found")
			}
			if key.Type != GTTagTypeShort {
				t.Errorf("key 3072 type: got %v want GTTagTypeShort", key.Type)
			}
			if key.AsShort() != 26915 {
				t.Errorf("key 3072 value: got %d want 26915", key.AsShort())
			}
			if key.Name() != "ProjectedCSTypeGeoKey" {
				t.Errorf("key 3072 Name: got %q", key.Name())
			}
		})
	}
}

func TestGeoTIFF_ProjectedEPSG26916_WithDoubleParams(t *testing.T) {
	// epsg_26916.las has a double params VLR (recID 34736, 24 bytes = 3 doubles).
	// Key 2062 (GeogTowgs84GeoKey) is a double at offset 0.
	r := openVLR(t, "epsg_26916.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF: %v", err)
	}
	if geo == nil {
		t.Fatal("GeoTIFF() returned nil")
	}
	// Projected key.
	if key := geo.Keys[3072]; key == nil || key.AsShort() != 26916 {
		t.Errorf("ProjectedCSTypeGeoKey: expected 26916, got %v", geo.Keys[3072])
	}
	// Double key (GeogTowgs84GeoKey, key 2062) must have been resolved from 34736.
	if key := geo.Keys[2062]; key == nil {
		t.Error("GeogTowgs84GeoKey (2062) not found")
	} else if key.Type != GTTagTypeDouble {
		t.Errorf("key 2062 type: got %v want GTTagTypeDouble", key.Type)
	}
}

func TestGeoTIFF_ProjectedEPSG32617(t *testing.T) {
	r := openVLR(t, "epsg_32617.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF: %v", err)
	}
	if geo == nil {
		t.Fatal("GeoTIFF() returned nil")
	}
	if key := geo.Keys[3072]; key == nil || key.AsShort() != 32617 {
		t.Errorf("ProjectedCSTypeGeoKey: expected 32617, got %v", geo.Keys[3072])
	}
}

func TestGeoTIFF_GeographicEPSG4326(t *testing.T) {
	// epsg_4326.las: key 1024 = 2 (geographic model), key 2048 = 4326.
	r := openVLR(t, "epsg_4326.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF: %v", err)
	}
	if geo == nil {
		t.Fatal("GeoTIFF() returned nil")
	}
	// Should be geographic (no key 3072).
	if _, hasProj := geo.Keys[3072]; hasProj {
		t.Error("did not expect ProjectedCSTypeGeoKey for geographic file")
	}
	if key := geo.Keys[2048]; key == nil || key.AsShort() != 4326 {
		t.Errorf("GeographicTypeGeoKey: expected 4326, got %v", geo.Keys[2048])
	}
	// Key 1024 (GTModelTypeGeoKey) should be 2 (geographic).
	if key := geo.Keys[1024]; key == nil || key.AsShort() != 2 {
		t.Errorf("GTModelTypeGeoKey: expected 2, got %v", geo.Keys[1024])
	}
}

func TestGeoTIFF_EmptyDoubleAndASCII(t *testing.T) {
	// 1.2-empty-geotiff-vlrs.las has 34736 and 34737 present but with 0 bytes.
	// ParseGeoTIFF should succeed and skip keys that reference those VLRs.
	r := openVLR(t, "1.2-empty-geotiff-vlrs.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF with empty companion VLRs: %v", err)
	}
	// No crash; keys with loc=34736/34737 are simply absent from the map.
	_ = geo
}

func TestGeoTIFF_UserDefinedCodes(t *testing.T) {
	// lots_of_vlr.las has 32767 (user-defined sentinel) for keys 2048 and 3072.
	r := openVLR(t, "lots_of_vlr.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF: %v", err)
	}
	if geo == nil {
		t.Fatal("GeoTIFF() should return metadata (user-defined codes are still parsed)")
	}
	if key := geo.Keys[3072]; key != nil && key.AsShort() != 32767 {
		t.Errorf("expected 32767 (user-defined), got %d", key.AsShort())
	}
}

func TestGeoTIFF_Absent(t *testing.T) {
	// extrabytes.las has only LASF_Spec VLR — no projection.
	r := openVLR(t, "extrabytes.las")
	defer r.Close()
	geo, err := r.GeoTIFF()
	if err != nil {
		t.Fatalf("GeoTIFF on file without GeoTIFF: %v", err)
	}
	if geo != nil {
		t.Errorf("expected nil GeoTIFF for file without projection VLR, got %+v", geo)
	}
}

// ---------------------------------------------------------------------------
// CRS() helper tests
// ---------------------------------------------------------------------------

func TestCRS_WKTWins_Over_GeoTIFF(t *testing.T) {
	// test_epsg_4047.las has both WKT (2112) and GeoTIFF (key 2048=4047).
	// WKT must take precedence.
	r := openVLR(t, "test_epsg_4047.las")
	defer r.Close()
	crs := r.CRS()
	if crs == "" {
		t.Fatal("CRS() returned empty string")
	}
	// Result must be the WKT string, not "EPSG:4047".
	if crs == "EPSG:4047" {
		t.Error("CRS() returned EPSG code but WKT should have taken precedence")
	}
	if !strings.Contains(crs, "GRS 1980 Authalic Sphere") {
		t.Errorf("CRS() should return WKT string; got prefix: %q", crs[:min(80, len(crs))])
	}
}

func TestCRS_GeoTIFF_Projected_26915(t *testing.T) {
	for _, name := range []string{
		"1.1_0_epsg26915.las",
		"1.2_0_epsg26915.las",
		"1.2_3_epsg26915.las",
	} {
		t.Run(name, func(t *testing.T) {
			r := openVLR(t, name)
			defer r.Close()
			if got := r.CRS(); got != "EPSG:26915" {
				t.Errorf("CRS(): got %q want %q", got, "EPSG:26915")
			}
		})
	}
}

func TestCRS_GeoTIFF_Projected_26916(t *testing.T) {
	r := openVLR(t, "epsg_26916.las")
	defer r.Close()
	if got := r.CRS(); got != "EPSG:26916" {
		t.Errorf("CRS(): got %q want %q", got, "EPSG:26916")
	}
}

func TestCRS_GeoTIFF_Projected_32617(t *testing.T) {
	r := openVLR(t, "epsg_32617.las")
	defer r.Close()
	if got := r.CRS(); got != "EPSG:32617" {
		t.Errorf("CRS(): got %q want %q", got, "EPSG:32617")
	}
}

func TestCRS_GeoTIFF_Geographic_4326(t *testing.T) {
	r := openVLR(t, "epsg_4326.las")
	defer r.Close()
	if got := r.CRS(); got != "EPSG:4326" {
		t.Errorf("CRS(): got %q want %q", got, "EPSG:4326")
	}
}

func TestCRS_UserDefinedCodes_WKT(t *testing.T) {
	// lots_of_vlr: both projected (3072=32767) and geographic (2048=32767)
	// are user-defined. CRS() should synthesize WKT from the GeoTIFF keys.
	r := openVLR(t, "lots_of_vlr.las")
	defer r.Close()
	got := r.CRS()
	if got == "" {
		t.Fatal("CRS() for user-defined codes returned empty string")
	}
	for _, want := range []string{
		`PROJCS["User-defined projected CRS"`,
		`PROJECTION["Transverse_Mercator"]`,
		`SPHEROID["GRS 1980"`,
		`UNIT["US survey foot"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CRS() synthesized WKT missing %q; got %q", want, got)
		}
	}
}

func TestGeoTIFF_CRS_GeoTIFF11EPSGRanges(t *testing.T) {
	geo := geoWithShorts(map[uint16]uint16{
		1024: 1,    // projected model type
		3072: 3857, // EPSG projected CRS below the old GeoTIFF 1.0 20000 range
	})
	if got := geo.CRS(); got != "EPSG:3857" {
		t.Errorf("GeoTIFF CRS(): got %q want %q", got, "EPSG:3857")
	}
}

func TestGeoTIFF_CRS_HorizontalVerticalEPSG(t *testing.T) {
	geo := geoWithShorts(map[uint16]uint16{
		1024: 2,    // geographic model type
		2048: 4326, // WGS 84
		4096: 5703, // NAVD88 height
	})
	if got := geo.CRS(); got != "EPSG:4326+5703" {
		t.Errorf("GeoTIFF CRS(): got %q want %q", got, "EPSG:4326+5703")
	}
}

func TestParseGeoTIFF_MultiValueKeys(t *testing.T) {
	// Header (4 shorts), 3 key entries (12 shorts), then 3 extra SHORT values.
	directory := packShorts(
		1, 1, 1, 3,
		1024, 0, 1, 1,
		2062, 34736, 3, 0,
		6000, 34735, 3, 16,
		7, 8, 9,
	)
	doubles := packDoubles(1.5, 2.5, 3.5)

	geo, err := ParseGeoTIFF(directory, doubles, nil)
	if err != nil {
		t.Fatalf("ParseGeoTIFF: %v", err)
	}
	if geo.MinorRevision != 1 {
		t.Errorf("MinorRevision: got %d want 1", geo.MinorRevision)
	}
	if got := geo.Keys[2062].AsDoubles(); len(got) != 3 || got[0] != 1.5 || got[2] != 3.5 {
		t.Errorf("double array: got %#v", got)
	}
	if got := geo.Keys[6000].AsShorts(); len(got) != 3 || got[0] != 7 || got[2] != 9 {
		t.Errorf("short array: got %#v", got)
	}
}

func TestCRS_EmptyGeoTIFF_Empty(t *testing.T) {
	// 1.2-empty-geotiff-vlrs.las: has GeoTIFF directory but no EPSG CRS keys
	// and no WKT — CRS() must return "".
	r := openVLR(t, "1.2-empty-geotiff-vlrs.las")
	defer r.Close()
	if got := r.CRS(); got != "" {
		t.Errorf("CRS() for file without CRS keys: got %q want %q", got, "")
	}
}

func TestCRS_NoCRS(t *testing.T) {
	r := openVLR(t, "extrabytes.las")
	defer r.Close()
	if got := r.CRS(); got != "" {
		t.Errorf("CRS() for file without projection: got %q want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// VLR count stress test
// ---------------------------------------------------------------------------

func TestVLRs_LotsOfVLRs(t *testing.T) {
	for _, name := range []string{"lots_of_vlr.las", "lots_of_vlr.laz"} {
		t.Run(name, func(t *testing.T) {
			r := openVLR(t, name)
			defer r.Close()
			// File has 390 (las) or 391 (laz) VLRs — all must be parsed.
			n := len(r.VLRs())
			if n < 390 {
				t.Errorf("expected ≥ 390 VLRs, got %d", n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GeoTIFF key name lookup
// ---------------------------------------------------------------------------

func TestGeoTIFFKeyName(t *testing.T) {
	cases := []struct {
		id   int
		name string
	}{
		{1024, "GTModelTypeGeoKey"},
		{2048, "GeographicTypeGeoKey"},
		{3072, "ProjectedCSTypeGeoKey"},
		{4096, "VerticalCSTypeGeoKey"},
		{9999, ""},
	}
	for _, tc := range cases {
		if got := GeoTIFFKeyName(tc.id); got != tc.name {
			t.Errorf("GeoTIFFKeyName(%d): got %q want %q", tc.id, got, tc.name)
		}
	}
}
