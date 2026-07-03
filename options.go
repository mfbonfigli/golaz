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

import laz "github.com/mfbonfigli/golaz/internal/laz"

// ---------------------------------------------------------------------------
// SelectiveMask — selective decompression
// ---------------------------------------------------------------------------

// SelectiveMask controls which point attributes are decompressed when reading
// LAS 1.4 files compressed with the v3/v4 layered compressor.
//
// Each bit corresponds to one compressed layer. When a bit is clear the
// decompressor skips that layer entirely — saving I/O and CPU — and the
// attribute is frozen at the value from the first (raw, uncompressed) point
// of each chunk.
//
// Masks can be combined with |:
//
//	golaz.SelectiveZ | golaz.SelectiveGPSTime
//
// For uncompressed files and LAS 1.2/1.3 pointwise-compressed files the mask
// is silently ignored and every attribute is always decompressed.
//
// The XY and scanner-channel/returns layer is always decoded regardless of the
// mask; X, Y, ReturnNumber, NumberOfReturns, and ScannerChannel are therefore
// always correct.
type SelectiveMask uint32

const (
	// SelectiveAll decodes every attribute. This is the default.
	SelectiveAll SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_ALL)

	// SelectiveZ decodes the Z (elevation) layer.
	SelectiveZ SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_Z)

	// SelectiveClassification decodes the classification byte.
	SelectiveClassification SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_CLASSIFICATION)

	// SelectiveFlags decodes the classification flags (synthetic, key-point,
	// withheld, overlap) and edge-of-flight-line / scan-direction bits.
	SelectiveFlags SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_FLAGS)

	// SelectiveIntensity decodes the intensity layer.
	SelectiveIntensity SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_INTENSITY)

	// SelectiveScanAngle decodes the scan angle layer.
	SelectiveScanAngle SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_SCAN_ANGLE)

	// SelectiveUserData decodes the user data byte.
	SelectiveUserData SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_USER_DATA)

	// SelectivePointSource decodes the point source ID.
	SelectivePointSource SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_POINT_SOURCE)

	// SelectiveGPSTime decodes the GPS time layer.
	SelectiveGPSTime SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_GPS_TIME)

	// SelectiveRGB decodes the red, green, and blue colour channels.
	SelectiveRGB SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_RGB)

	// SelectiveNIR decodes the near-infrared channel.
	SelectiveNIR SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_NIR)

	// SelectiveWavepacket decodes the full-waveform wavepacket layer.
	SelectiveWavepacket SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_WAVEPACKET)

	// SelectiveExtraBytes decodes all extra byte channels.
	SelectiveExtraBytes SelectiveMask = SelectiveMask(laz.LASZIP_DECOMPRESS_SELECTIVE_EXTRA_BYTES)
)

// ---------------------------------------------------------------------------
// Option — functional option for Open / OpenReader
// ---------------------------------------------------------------------------

// Option is a functional option passed to Open or OpenReader to customise
// Reader behaviour.
type Option func(*readerConfig)

// readerConfig holds the resolved options for a Reader.
type readerConfig struct {
	selectiveMask    SelectiveMask
	maskExplicitlySet bool
	compatibilityMode bool
}

func defaultConfig() readerConfig {
	return readerConfig{
		// selectiveMask defaults to SelectiveAll when not set.
		compatibilityMode: true,
	}
}

// WithSelectiveMask adds attributes to the selective decompression mask.
// Multiple calls accumulate: each mask is OR-ed with the previous ones.
//
//	// decode only Z and GPS time:
//	WithSelectiveMask(golaz.SelectiveZ | golaz.SelectiveGPSTime)
//
//	// equivalent using two separate calls:
//	WithSelectiveMask(golaz.SelectiveZ),
//	WithSelectiveMask(golaz.SelectiveGPSTime),
//
// Only LAS 1.4 files compressed with the v3/v4 layered compressor benefit
// from this option; for all other formats it is silently ignored.
func WithSelectiveMask(mask SelectiveMask) Option {
	return func(cfg *readerConfig) {
		if !cfg.maskExplicitlySet {
			// First call: replace the implicit SelectiveAll default.
			cfg.selectiveMask = mask
			cfg.maskExplicitlySet = true
		} else {
			// Subsequent calls: accumulate.
			cfg.selectiveMask |= mask
		}
	}
}

// WithCompatibilityMode controls LAS 1.4 compatibility-mode reconstruction
// for files written by `laszip -compatible` (point formats 6-10 recoded as
// formats 1/3/4/5 plus "LAS 1.4 ..." extra-byte attributes and a
// lascompatible VLR).
//
// When enabled (the default), such files are transparently presented as
// native LAS 1.4: the header reports the original point format 6-10, points
// carry 16-bit scan angles, 4-bit return counts, full 8-bit classifications,
// scanner channel, overlap flag, and NIR, and the compatibility attributes
// are hidden from ExtraBytes. When disabled, the file is read exactly as
// stored on disk (format 1/3/4/5 with raw extra bytes).
//
// Note: the LASzip C DLL requires an explicit laszip_request_compatibility_mode
// call to enable reconstruction; golaz enables it by default because the
// reconstructed form is what such files are meant to represent.
func WithCompatibilityMode(enabled bool) Option {
	return func(cfg *readerConfig) {
		cfg.compatibilityMode = enabled
	}
}
