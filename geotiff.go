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
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// GeoTIFF projection metadata (LASF_Projection recIDs 34735/34736/34737)
// ---------------------------------------------------------------------------

// GeoTIFFTagType describes the value type stored inside a GeoTIFFKey.
type GeoTIFFTagType int

const (
	GTTagTypeShort  GeoTIFFTagType = 0 // uint16 value from the key directory
	GTTagTypeDouble GeoTIFFTagType = 1 // float64 value from the doubles VLR (recID 34736)
	GTTagTypeString GeoTIFFTagType = 2 // string value from the ASCII VLR (recID 34737)
)

// GeoTIFFKey is one resolved entry from the GeoTIFF key directory.
type GeoTIFFKey struct {
	KeyID    uint16
	Type     GeoTIFFTagType
	Count    uint16
	rawValue any // uint16 | []uint16 | float64 | []float64 | string
}

// Name returns the well-known name of this key, or "" if unknown.
func (k *GeoTIFFKey) Name() string {
	return GeoTIFFKeyName(int(k.KeyID))
}

// AsShort returns the key value as uint16.
// Only valid when Type == GTTagTypeShort.
func (k *GeoTIFFKey) AsShort() uint16 {
	switch v := k.rawValue.(type) {
	case uint16:
		return v
	case []uint16:
		return v[0]
	default:
		panic("GeoTIFFKey is not a SHORT value")
	}
}

// AsShorts returns the key value as a slice of uint16 values.
// Only valid when Type == GTTagTypeShort.
func (k *GeoTIFFKey) AsShorts() []uint16 {
	switch v := k.rawValue.(type) {
	case uint16:
		return []uint16{v}
	case []uint16:
		out := make([]uint16, len(v))
		copy(out, v)
		return out
	default:
		panic("GeoTIFFKey is not a SHORT value")
	}
}

// AsDouble returns the key value as float64.
// Only valid when Type == GTTagTypeDouble.
func (k *GeoTIFFKey) AsDouble() float64 {
	switch v := k.rawValue.(type) {
	case float64:
		return v
	case []float64:
		return v[0]
	default:
		panic("GeoTIFFKey is not a DOUBLE value")
	}
}

// AsDoubles returns the key value as a slice of float64 values.
// Only valid when Type == GTTagTypeDouble.
func (k *GeoTIFFKey) AsDoubles() []float64 {
	switch v := k.rawValue.(type) {
	case float64:
		return []float64{v}
	case []float64:
		out := make([]float64, len(v))
		copy(out, v)
		return out
	default:
		panic("GeoTIFFKey is not a DOUBLE value")
	}
}

// AsString returns the key value as string.
// Only valid when Type == GTTagTypeString.
func (k *GeoTIFFKey) AsString() string { return k.rawValue.(string) }

// AsStrings returns the key value split on the GeoTIFF ASCII pipe separator.
// Only valid when Type == GTTagTypeString.
func (k *GeoTIFFKey) AsStrings() []string {
	s := k.AsString()
	if s == "" {
		return nil
	}
	return strings.Split(s, "|")
}

// GeoTIFFMetadata holds the fully resolved GeoTIFF key directory.
type GeoTIFFMetadata struct {
	DirectoryVersion uint16
	KeyRevision      uint16
	MinorRevision    uint16
	Keys             map[uint16]*GeoTIFFKey
}

// ParseGeoTIFF parses GeoTIFF projection metadata from the three companion
// VLR payloads. directoryData (recID 34735) is required; doubleParamsData
// (recID 34736) and asciiParamsData (recID 34737) may be nil or empty.
//
// Each key's value is fully resolved by looking up shorts, doubles, and strings
// in the key directory and companion slices. Version validation is tolerant:
// unexpected directory version numbers are accepted and parsing continues.
func ParseGeoTIFF(directoryData, doubleParamsData, asciiParamsData []byte) (*GeoTIFFMetadata, error) {
	if len(directoryData) < 8 {
		return nil, fmt.Errorf("GeoTIFF directory VLR too short (%d bytes, need >= 8)", len(directoryData))
	}
	if len(directoryData)%2 != 0 {
		return nil, fmt.Errorf("GeoTIFF directory VLR has odd byte length %d", len(directoryData))
	}

	dirShorts := make([]uint16, len(directoryData)/2)
	for i := range dirShorts {
		dirShorts[i] = binary.LittleEndian.Uint16(directoryData[i*2:])
	}

	dirVersion := dirShorts[0]
	keyRevision := dirShorts[1]
	minorRevision := dirShorts[2]
	numKeys := dirShorts[3]

	geo := &GeoTIFFMetadata{
		DirectoryVersion: dirVersion,
		KeyRevision:      keyRevision,
		MinorRevision:    minorRevision,
		Keys:             make(map[uint16]*GeoTIFFKey, numKeys),
	}

	for i := range numKeys {
		off := 8 + int(i)*8
		if off+8 > len(directoryData) {
			break
		}
		keyID := binary.LittleEndian.Uint16(directoryData[off:])
		location := binary.LittleEndian.Uint16(directoryData[off+2:])
		count := binary.LittleEndian.Uint16(directoryData[off+4:])
		valueOffset := binary.LittleEndian.Uint16(directoryData[off+6:])

		var key *GeoTIFFKey

		switch location {
		case 0:
			// Inline SHORT value. GeoTIFF requires count=1 for inline values.
			if count == 1 {
				key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeShort, Count: count, rawValue: valueOffset}
			}

		case 34735:
			// SHORT array stored in the GeoKeyDirectoryTag after the key entries.
			start := int(valueOffset)
			end := start + int(count)
			if count > 0 && start <= end && end <= len(dirShorts) {
				vals := make([]uint16, count)
				copy(vals, dirShorts[start:end])
				if count == 1 {
					key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeShort, Count: count, rawValue: vals[0]}
				} else {
					key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeShort, Count: count, rawValue: vals}
				}
			}

		case 34736:
			// Float64 array from the doubles VLR; valueOffset is an index, not bytes.
			byteOff := int(valueOffset) * 8
			byteEnd := byteOff + int(count)*8
			if count > 0 && byteOff <= byteEnd && byteEnd <= len(doubleParamsData) {
				vals := make([]float64, count)
				for j := range vals {
					start := byteOff + j*8
					vals[j] = math.Float64frombits(binary.LittleEndian.Uint64(doubleParamsData[start : start+8]))
				}
				if count == 1 {
					key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeDouble, Count: count, rawValue: vals[0]}
				} else {
					key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeDouble, Count: count, rawValue: vals}
				}
			}

		case 34737:
			// ASCII string from the ASCII VLR; valueOffset+count gives the slice.
			start := int(valueOffset)
			end := start + int(count)
			if count > 0 && start <= end && end <= len(asciiParamsData) {
				s := strings.TrimRight(string(asciiParamsData[start:end]), "\x00")
				s = strings.TrimRight(s, "|")
				key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeString, Count: count, rawValue: s}
			}
		}

		if key != nil {
			geo.Keys[keyID] = key
		}
	}

	return geo, nil
}

// HorizontalEPSG returns the horizontal EPSG CRS code from the GeoTIFF keys.
//
// GeoTIFF 1.1 allows ProjectedCRSGeoKey and GeodeticCRSGeoKey values in the
// range 1024-32766 to be EPSG CRS codes. Projected CRS keys take precedence for
// projected model types; geographic/geodetic keys are used for geographic and
// geocentric model types or as a fallback when no projected CRS is present.
func (g *GeoTIFFMetadata) HorizontalEPSG() (uint16, bool) {
	if g == nil {
		return 0, false
	}
	model := g.shortValue(1024)
	if model == 2 || model == 3 {
		if code, ok := g.standardShort(2048); ok {
			return code, true
		}
		if code, ok := g.standardShort(3072); ok {
			return code, true
		}
		return 0, false
	}
	if code, ok := g.standardShort(3072); ok {
		return code, true
	}
	if model != 1 {
		if code, ok := g.standardShort(2048); ok {
			return code, true
		}
	}
	return 0, false
}

// VerticalEPSG returns the vertical EPSG CRS code from VerticalCSTypeGeoKey.
//
// GeoTIFF 1.1 uses 1024-32766 for EPSG vertical CRS or geographic 3D CRS codes.
func (g *GeoTIFFMetadata) VerticalEPSG() (uint16, bool) {
	if g == nil {
		return 0, false
	}
	return g.standardShort(4096)
}

// CRS returns the CRS encoded by the GeoTIFF keys.
//
// The result is "EPSG:<horizontal>" when a standard horizontal CRS code is
// available, "EPSG:<horizontal>+<vertical>" when both horizontal and vertical
// EPSG CRS codes are available, or a WKT string synthesized from user-defined
// GeoTIFF keys when no horizontal EPSG CRS code is available.
func (g *GeoTIFFMetadata) CRS() string {
	if g == nil {
		return ""
	}
	if hor, ok := g.HorizontalEPSG(); ok {
		if vert, ok := g.VerticalEPSG(); ok {
			return fmt.Sprintf("EPSG:%d+%d", hor, vert)
		}
		return fmt.Sprintf("EPSG:%d", hor)
	}
	return g.WKT()
}

// WKT returns an OGC WKT1 CRS synthesized from GeoTIFF CRS keys.
//
// GeoTIFF has no embedded WKT key; this method derives WKT from the standard
// user-defined CRS keys, citations, unit keys, ellipsoid keys, projection
// method keys, and projection parameter keys. It returns an empty string when
// the key set is too sparse to describe a CRS.
func (g *GeoTIFFMetadata) WKT() string {
	if g == nil {
		return ""
	}

	horizontal := ""
	if g.isProjected() {
		horizontal = g.projectedWKT()
	} else if g.isGeodetic() {
		horizontal = g.geogWKT()
	}

	vertical := g.verticalWKT()
	if horizontal != "" && vertical != "" {
		return fmt.Sprintf("COMPD_CS[%s,%s,%s]", quoteWKT(g.compoundName()), horizontal, vertical)
	}
	if horizontal != "" {
		return horizontal
	}
	return vertical
}

func (g *GeoTIFFMetadata) standardShort(keyID uint16) (uint16, bool) {
	code := g.shortValue(keyID)
	if isGeoTIFFStandardEPSG(code) {
		return code, true
	}
	return 0, false
}

func (g *GeoTIFFMetadata) shortValue(keyID uint16) uint16 {
	key := g.Keys[keyID]
	if key == nil || key.Type != GTTagTypeShort {
		return 0
	}
	return key.AsShort()
}

func (g *GeoTIFFMetadata) doubleValue(keyID uint16) (float64, bool) {
	key := g.Keys[keyID]
	if key == nil || key.Type != GTTagTypeDouble {
		return 0, false
	}
	return key.AsDouble(), true
}

func (g *GeoTIFFMetadata) stringValue(keyID uint16) string {
	key := g.Keys[keyID]
	if key == nil || key.Type != GTTagTypeString {
		return ""
	}
	return key.AsString()
}

func isGeoTIFFStandardEPSG(code uint16) bool {
	return code >= 1024 && code <= 32766
}

func isGeoTIFFUserDefined(code uint16) bool {
	return code == 32767
}

func (g *GeoTIFFMetadata) isProjected() bool {
	model := g.shortValue(1024)
	return model == 1 || isGeoTIFFUserDefined(g.shortValue(3072)) || g.shortValue(3075) != 0 || g.shortValue(3074) != 0
}

func (g *GeoTIFFMetadata) isGeodetic() bool {
	model := g.shortValue(1024)
	return model == 2 || model == 3 || isGeoTIFFUserDefined(g.shortValue(2048)) || g.shortValue(2048) != 0 || g.shortValue(2050) != 0
}

func (g *GeoTIFFMetadata) projectedWKT() string {
	methodCode := g.shortValue(3075)
	method, ok := projectionMethodWKTNames[methodCode]
	if !ok {
		return ""
	}

	geog := g.geogWKT()
	if geog == "" {
		return ""
	}

	name := firstNonEmpty(g.stringValue(3073), g.stringValue(1026), "User-defined projected CRS")
	unit := g.linearUnit(3076, 3077)

	var b strings.Builder
	b.WriteString("PROJCS[")
	b.WriteString(quoteWKT(cleanCitation(name)))
	b.WriteByte(',')
	b.WriteString(geog)
	b.WriteString(",PROJECTION[")
	b.WriteString(quoteWKT(method))
	b.WriteByte(']')
	for _, param := range projectionWKTParams {
		if v, ok := g.doubleValue(param.keyID); ok {
			b.WriteString(",PARAMETER[")
			b.WriteString(quoteWKT(param.name))
			b.WriteByte(',')
			b.WriteString(formatWKTFloat(v))
			b.WriteByte(']')
		}
	}
	b.WriteByte(',')
	b.WriteString(unit.wkt("UNIT"))
	if code := g.shortValue(3072); isGeoTIFFStandardEPSG(code) {
		b.WriteString(authorityWKT(code))
	}
	b.WriteByte(']')
	return b.String()
}

func (g *GeoTIFFMetadata) geogWKT() string {
	if code := g.shortValue(2048); isGeoTIFFStandardEPSG(code) {
		if wkt, ok := geographicCRSWKT(code); ok {
			return wkt
		}
	}

	name := firstNonEmpty(g.stringValue(2049), g.stringValue(1026), "User-defined geographic CRS")
	datum := g.datumWKT()
	if datum == "" {
		return ""
	}
	unit := g.angularUnit(2054, 2055)

	var b strings.Builder
	b.WriteString("GEOGCS[")
	b.WriteString(quoteWKT(cleanCitation(name)))
	b.WriteByte(',')
	b.WriteString(datum)
	b.WriteByte(',')
	b.WriteString(g.primeMeridianWKT())
	b.WriteByte(',')
	b.WriteString(unit.wkt("UNIT"))
	if code := g.shortValue(2048); isGeoTIFFStandardEPSG(code) {
		b.WriteString(authorityWKT(code))
	}
	b.WriteByte(']')
	return b.String()
}

func (g *GeoTIFFMetadata) datumWKT() string {
	code := g.shortValue(2050)
	if def, ok := datumDefs[code]; ok {
		ellipsoid := ellipsoidDefs[def.ellipsoid]
		return fmt.Sprintf("DATUM[%s,%s%s]", quoteWKT(def.name), ellipsoid.wkt(), authorityWKT(code))
	}

	name := firstNonEmpty(g.stringValue(2049), "User-defined datum")
	ellipsoid := g.ellipsoidWKT()
	if ellipsoid == "" {
		return ""
	}
	if isGeoTIFFStandardEPSG(code) {
		return fmt.Sprintf("DATUM[%s,%s%s]", quoteWKT(cleanCitation(name)), ellipsoid, authorityWKT(code))
	}
	return fmt.Sprintf("DATUM[%s,%s]", quoteWKT(cleanCitation(name)), ellipsoid)
}

func (g *GeoTIFFMetadata) ellipsoidWKT() string {
	code := g.shortValue(2056)
	if def, ok := ellipsoidDefs[code]; ok {
		return def.wkt()
	}

	semiMajor, ok := g.doubleValue(2057)
	if !ok {
		return ""
	}
	invFlattening, hasInvFlattening := g.doubleValue(2059)
	if !hasInvFlattening {
		if semiMinor, hasSemiMinor := g.doubleValue(2058); hasSemiMinor && semiMajor != semiMinor {
			invFlattening = semiMajor / (semiMajor - semiMinor)
		}
	}
	name := firstNonEmpty(g.stringValue(2049), "User-defined ellipsoid")

	var b strings.Builder
	b.WriteString("SPHEROID[")
	b.WriteString(quoteWKT(cleanCitation(name)))
	b.WriteByte(',')
	b.WriteString(formatWKTFloat(semiMajor))
	b.WriteByte(',')
	if hasInvFlattening || invFlattening != 0 {
		b.WriteString(formatWKTFloat(invFlattening))
	} else {
		b.WriteByte('0')
	}
	if isGeoTIFFStandardEPSG(code) {
		b.WriteString(authorityWKT(code))
	}
	b.WriteByte(']')
	return b.String()
}

func (g *GeoTIFFMetadata) primeMeridianWKT() string {
	code := g.shortValue(2051)
	if def, ok := primeMeridianDefs[code]; ok {
		return fmt.Sprintf("PRIMEM[%s,%s%s]", quoteWKT(def.name), formatWKTFloat(def.longitude), authorityWKT(code))
	}
	longitude, ok := g.doubleValue(2061)
	if !ok {
		longitude = 0
	}
	name := "Greenwich"
	if isGeoTIFFUserDefined(code) {
		name = cleanCitation(firstNonEmpty(g.stringValue(2049), "User-defined prime meridian"))
	}
	if isGeoTIFFStandardEPSG(code) {
		return fmt.Sprintf("PRIMEM[%s,%s%s]", quoteWKT(name), formatWKTFloat(longitude), authorityWKT(code))
	}
	return fmt.Sprintf("PRIMEM[%s,%s]", quoteWKT(name), formatWKTFloat(longitude))
}

func (g *GeoTIFFMetadata) verticalWKT() string {
	code := g.shortValue(4096)
	if code == 0 {
		return ""
	}
	name := firstNonEmpty(g.stringValue(4097), fmt.Sprintf("EPSG:%d", code))
	unit := g.linearUnit(4099, 0)
	datumCode := g.shortValue(4098)
	datumName := firstNonEmpty(g.stringValue(4097), "User-defined vertical datum")

	var b strings.Builder
	b.WriteString("VERT_CS[")
	b.WriteString(quoteWKT(cleanCitation(name)))
	b.WriteString(",VERT_DATUM[")
	b.WriteString(quoteWKT(cleanCitation(datumName)))
	b.WriteString(",2005")
	if isGeoTIFFStandardEPSG(datumCode) {
		b.WriteString(authorityWKT(datumCode))
	}
	b.WriteByte(']')
	b.WriteByte(',')
	b.WriteString(unit.wkt("UNIT"))
	if isGeoTIFFStandardEPSG(code) {
		b.WriteString(authorityWKT(code))
	}
	b.WriteByte(']')
	return b.String()
}

func (g *GeoTIFFMetadata) linearUnit(codeKeyID, sizeKeyID uint16) wktUnit {
	code := g.shortValue(codeKeyID)
	if def, ok := linearUnitDefs[code]; ok {
		return def
	}
	if isGeoTIFFUserDefined(code) && sizeKeyID != 0 {
		if size, ok := g.doubleValue(sizeKeyID); ok {
			return wktUnit{name: "user-defined linear unit", conv: size}
		}
	}
	return linearUnitDefs[9001]
}

func (g *GeoTIFFMetadata) angularUnit(codeKeyID, sizeKeyID uint16) wktUnit {
	code := g.shortValue(codeKeyID)
	if def, ok := angularUnitDefs[code]; ok {
		return def
	}
	if isGeoTIFFUserDefined(code) && sizeKeyID != 0 {
		if size, ok := g.doubleValue(sizeKeyID); ok {
			return wktUnit{name: "user-defined angular unit", conv: size}
		}
	}
	return angularUnitDefs[9102]
}

func (g *GeoTIFFMetadata) compoundName() string {
	return firstNonEmpty(g.stringValue(1026), g.stringValue(3073), g.stringValue(2049), "Compound CRS")
}

type wktUnit struct {
	name string
	conv float64
	code uint16
}

func (u wktUnit) wkt(keyword string) string {
	s := fmt.Sprintf("%s[%s,%s", keyword, quoteWKT(u.name), formatWKTFloat(u.conv))
	if u.code != 0 {
		s += authorityWKT(u.code)
	}
	return s + "]"
}

type ellipsoidDef struct {
	name          string
	semiMajor     float64
	invFlattening float64
	code          uint16
}

func (e ellipsoidDef) wkt() string {
	return fmt.Sprintf("SPHEROID[%s,%s,%s%s]", quoteWKT(e.name), formatWKTFloat(e.semiMajor), formatWKTFloat(e.invFlattening), authorityWKT(e.code))
}

type datumDef struct {
	name      string
	ellipsoid uint16
}

type primeMeridianDef struct {
	name      string
	longitude float64
}

type projectionParam struct {
	keyID uint16
	name  string
}

var linearUnitDefs = map[uint16]wktUnit{
	9001: {name: "metre", conv: 1, code: 9001},
	9002: {name: "foot", conv: 0.3048, code: 9002},
	9003: {name: "US survey foot", conv: 1200.0 / 3937.0, code: 9003},
}

var angularUnitDefs = map[uint16]wktUnit{
	9101: {name: "radian", conv: 1, code: 9101},
	9102: {name: "degree", conv: math.Pi / 180, code: 9102},
	9103: {name: "arc-minute", conv: math.Pi / 10800, code: 9103},
	9104: {name: "arc-second", conv: math.Pi / 648000, code: 9104},
	9105: {name: "grad", conv: math.Pi / 200, code: 9105},
	9106: {name: "gon", conv: math.Pi / 200, code: 9106},
}

var ellipsoidDefs = map[uint16]ellipsoidDef{
	7001: {name: "Airy 1830", semiMajor: 6377563.396, invFlattening: 299.3249646, code: 7001},
	7004: {name: "Bessel 1841", semiMajor: 6377397.155, invFlattening: 299.1528128, code: 7004},
	7008: {name: "Clarke 1866", semiMajor: 6378206.4, invFlattening: 294.9786982, code: 7008},
	7019: {name: "GRS 1980", semiMajor: 6378137, invFlattening: 298.257222101, code: 7019},
	7022: {name: "International 1924", semiMajor: 6378388, invFlattening: 297, code: 7022},
	7030: {name: "WGS 84", semiMajor: 6378137, invFlattening: 298.257223563, code: 7030},
	7035: {name: "Sphere", semiMajor: 6371000, invFlattening: 0, code: 7035},
	7048: {name: "GRS 1980 Authalic Sphere", semiMajor: 6371007, invFlattening: 0, code: 7048},
}

var datumDefs = map[uint16]datumDef{
	6267: {name: "North American Datum 1927", ellipsoid: 7008},
	6269: {name: "North American Datum 1983", ellipsoid: 7019},
	6326: {name: "World Geodetic System 1984", ellipsoid: 7030},
}

var primeMeridianDefs = map[uint16]primeMeridianDef{
	8901: {name: "Greenwich", longitude: 0},
}

var projectionMethodWKTNames = map[uint16]string{
	1:  "Transverse_Mercator",
	2:  "Transverse_Mercator",
	3:  "Hotine_Oblique_Mercator",
	4:  "Laborde_Oblique_Mercator",
	5:  "Hotine_Oblique_Mercator",
	6:  "Oblique_Mercator",
	7:  "Mercator_1SP",
	8:  "Lambert_Conformal_Conic_2SP",
	9:  "Lambert_Conformal_Conic_1SP",
	10: "Lambert_Azimuthal_Equal_Area",
	11: "Albers_Conic_Equal_Area",
	12: "Azimuthal_Equidistant",
	13: "Equidistant_Conic",
	14: "Stereographic",
	15: "Polar_Stereographic",
	16: "Oblique_Stereographic",
	17: "Equirectangular",
	18: "Cassini_Soldner",
	19: "Gnomonic",
	20: "Miller_Cylindrical",
	21: "Orthographic",
	22: "Polyconic",
	23: "Robinson",
	24: "Sinusoidal",
	25: "VanDerGrinten",
	26: "New_Zealand_Map_Grid",
	27: "Transverse_Mercator_South_Orientated",
}

var projectionWKTParams = []projectionParam{
	{keyID: 3078, name: "standard_parallel_1"},
	{keyID: 3079, name: "standard_parallel_2"},
	{keyID: 3080, name: "central_meridian"},
	{keyID: 3081, name: "latitude_of_origin"},
	{keyID: 3082, name: "false_easting"},
	{keyID: 3083, name: "false_northing"},
	{keyID: 3084, name: "longitude_of_false_origin"},
	{keyID: 3085, name: "latitude_of_false_origin"},
	{keyID: 3086, name: "easting_at_false_origin"},
	{keyID: 3087, name: "northing_at_false_origin"},
	{keyID: 3088, name: "longitude_of_center"},
	{keyID: 3089, name: "latitude_of_center"},
	{keyID: 3090, name: "easting_at_center"},
	{keyID: 3091, name: "northing_at_center"},
	{keyID: 3092, name: "scale_factor"},
	{keyID: 3093, name: "scale_factor"},
	{keyID: 3094, name: "azimuth"},
	{keyID: 3095, name: "straight_vertical_longitude_from_pole"},
}

func geographicCRSWKT(code uint16) (string, bool) {
	switch code {
	case 4267:
		return geographicCRSWKTFromDatum("NAD27", code, 6267), true
	case 4269:
		return geographicCRSWKTFromDatum("NAD83", code, 6269), true
	case 4326:
		return geographicCRSWKTFromDatum("WGS 84", code, 6326), true
	}
	return "", false
}

func geographicCRSWKTFromDatum(name string, crsCode, datumCode uint16) string {
	def := datumDefs[datumCode]
	ellipsoid := ellipsoidDefs[def.ellipsoid]
	return fmt.Sprintf("GEOGCS[%s,DATUM[%s,%s%s],PRIMEM[%s,0%s],UNIT[%s,%s%s]%s]",
		quoteWKT(name),
		quoteWKT(def.name),
		ellipsoid.wkt(),
		authorityWKT(datumCode),
		quoteWKT("Greenwich"),
		authorityWKT(8901),
		quoteWKT("degree"),
		formatWKTFloat(math.Pi/180),
		authorityWKT(9102),
		authorityWKT(crsCode),
	)
}

func authorityWKT(code uint16) string {
	if code == 0 {
		return ""
	}
	return fmt.Sprintf(",AUTHORITY[%s,%s]", quoteWKT("EPSG"), quoteWKT(strconv.Itoa(int(code))))
}

func cleanCitation(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '|'); i >= 0 {
		s = s[:i]
	}
	return firstNonEmpty(strings.TrimSpace(s), "unnamed")
}

func quoteWKT(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func formatWKTFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', 15, 64)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Well-known GeoTIFF key names (GeoTIFF 1.0 and 1.1)
// ---------------------------------------------------------------------------

// GeoTIFFKeyName returns the standard name for a GeoTIFF key ID, or "" if unknown.
func GeoTIFFKeyName(keyID int) string {
	return geotiffKeyNames[keyID]
}

var geotiffKeyNames = map[int]string{
	// Configuration keys
	1024: "GTModelTypeGeoKey",
	1025: "GTRasterTypeGeoKey",
	1026: "GTCitationGeoKey",

	// Geographic/geodetic CS keys
	2048: "GeographicTypeGeoKey",
	2049: "GeogCitationGeoKey",
	2050: "GeogGeodeticDatumGeoKey",
	2051: "GeogPrimeMeridianGeoKey",
	2052: "GeogLinearUnitsGeoKey",
	2053: "GeogLinearUnitSizeGeoKey",
	2054: "GeogAngularUnitsGeoKey",
	2055: "GeogAngularUnitSizeGeoKey",
	2056: "GeogEllipsoidGeoKey",
	2057: "GeogSemiMajorAxisGeoKey",
	2058: "GeogSemiMinorAxisGeoKey",
	2059: "GeogInvFlatteningGeoKey",
	2060: "GeogAzimuthUnitsGeoKey",
	2061: "GeogPrimeMeridianLongGeoKey",
	2062: "GeogTowgs84GeoKey",

	// Projected CS keys
	3072: "ProjectedCSTypeGeoKey",
	3073: "PCSCitationGeoKey",
	3074: "ProjectionGeoKey",
	3075: "ProjCoordTransGeoKey",
	3076: "ProjLinearUnitsGeoKey",
	3077: "ProjLinearUnitSizeGeoKey",
	3078: "ProjStdParallel1GeoKey",
	3079: "ProjStdParallel2GeoKey",
	3080: "ProjNatOriginLongGeoKey",
	3081: "ProjNatOriginLatGeoKey",
	3082: "ProjFalseEastingGeoKey",
	3083: "ProjFalseNorthingGeoKey",
	3084: "ProjFalseOriginLongGeoKey",
	3085: "ProjFalseOriginLatGeoKey",
	3086: "ProjFalseOriginEastingGeoKey",
	3087: "ProjFalseOriginNorthingGeoKey",
	3088: "ProjCenterLongGeoKey",
	3089: "ProjCenterLatGeoKey",
	3090: "ProjCenterEastingGeoKey",
	3091: "ProjCenterNorthingGeoKey",
	3092: "ProjScaleAtNatOriginGeoKey",
	3093: "ProjScaleAtCenterGeoKey",
	3094: "ProjAzimuthAngleGeoKey",
	3095: "ProjStraightVertPoleLongGeoKey",

	// Vertical CS keys
	4096: "VerticalCSTypeGeoKey",
	4097: "VerticalCitationGeoKey",
	4098: "VerticalDatumGeoKey",
	4099: "VerticalUnitsGeoKey",
}
