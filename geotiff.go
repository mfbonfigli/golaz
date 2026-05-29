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
	"strings"
)

// ---------------------------------------------------------------------------
// GeoTIFF projection metadata (LASF_Projection recIDs 34735/34736/34737)
// ---------------------------------------------------------------------------

// GeoTIFFTagType describes the value type stored inside a GeoTIFFKey.
type GeoTIFFTagType int

const (
	GTTagTypeShort  GeoTIFFTagType = 0 // uint16 value stored inline in the key directory
	GTTagTypeDouble GeoTIFFTagType = 1 // float64 from the doubles VLR (recID 34736)
	GTTagTypeString GeoTIFFTagType = 2 // string from the ASCII VLR (recID 34737)
)

// GeoTIFFKey is one resolved entry from the GeoTIFF key directory.
type GeoTIFFKey struct {
	KeyID    uint16
	Type     GeoTIFFTagType
	rawValue any // uint16 | float64 | string
}

// Name returns the well-known name of this key, or "" if unknown.
func (k *GeoTIFFKey) Name() string {
	return GeoTIFFKeyName(int(k.KeyID))
}

// AsShort returns the key value as uint16.
// Only valid when Type == GTTagTypeShort.
func (k *GeoTIFFKey) AsShort() uint16 { return k.rawValue.(uint16) }

// AsDouble returns the key value as float64.
// Only valid when Type == GTTagTypeDouble.
func (k *GeoTIFFKey) AsDouble() float64 { return k.rawValue.(float64) }

// AsString returns the key value as string.
// Only valid when Type == GTTagTypeString.
func (k *GeoTIFFKey) AsString() string { return k.rawValue.(string) }

// GeoTIFFMetadata holds the fully resolved GeoTIFF key directory.
type GeoTIFFMetadata struct {
	DirectoryVersion uint16
	Keys             map[uint16]*GeoTIFFKey
}

// ParseGeoTIFF parses GeoTIFF projection metadata from the three companion
// VLR payloads. directoryData (recID 34735) is required; doubleParamsData
// (recID 34736) and asciiParamsData (recID 34737) may be nil or empty.
//
// Each key's value is fully resolved by looking up doubles and strings in
// the companion slices. Version validation is tolerant: unexpected directory
// version numbers are accepted and parsing continues regardless.
func ParseGeoTIFF(directoryData, doubleParamsData, asciiParamsData []byte) (*GeoTIFFMetadata, error) {
	if len(directoryData) < 8 {
		return nil, fmt.Errorf("GeoTIFF directory VLR too short (%d bytes, need ≥ 8)", len(directoryData))
	}

	dirVersion := binary.LittleEndian.Uint16(directoryData[0:2])
	// bytes 2–5: key revision (ignored for tolerance)
	numKeys := binary.LittleEndian.Uint16(directoryData[6:8])

	geo := &GeoTIFFMetadata{
		DirectoryVersion: dirVersion,
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
			// Inline short value; count is always 1 for shorts.
			key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeShort, rawValue: valueOffset}

		case 34736:
			// Float64 from the doubles VLR; valueOffset is an index (not bytes).
			byteOff := int(valueOffset) * 8
			if byteOff+8 <= len(doubleParamsData) {
				v := math.Float64frombits(binary.LittleEndian.Uint64(doubleParamsData[byteOff : byteOff+8]))
				key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeDouble, rawValue: v}
			}

		case 34737:
			// ASCII string from the ASCII VLR; valueOffset+count gives the slice.
			start := int(valueOffset)
			end := start + int(count)
			if start <= end && end <= len(asciiParamsData) {
				s := strings.TrimRight(string(asciiParamsData[start:end]), "\x00|")
				key = &GeoTIFFKey{KeyID: keyID, Type: GTTagTypeString, rawValue: s}
			}
		}

		if key != nil {
			geo.Keys[keyID] = key
		}
	}

	return geo, nil
}

// ---------------------------------------------------------------------------
// Well-known GeoTIFF key names (GeoTIFF 1.0 specification)
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

	// Geographic CS keys
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
