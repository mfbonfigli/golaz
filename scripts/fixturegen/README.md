# fixturegen — C++ LASzip test-fixture generator

Generates test fixtures for the golaz reader with the **real LASzip C++
reference implementation** (not synthesized ad hoc), pinned to upstream commit

    https://github.com/LASzip/LASzip.git
    ac3e9e9f954427872c8c77b68b7766cf706322f9

The single-file harness `genfixtures.cpp` is compiled inside Docker directly
against the LASzip sources. Groups A-D use the core classes `LASzip`,
`LASzipper`, `LASunzipper` and `LASreadPoint`; Group E uses the DLL API
(`laszip_dll.cpp`, compiled statically into the harness) because the LAS 1.4
compatibility-mode recoding lives in the DLL layer.

## How to regenerate

Requires Docker.

```sh
# from Git Bash / Linux / macOS
scripts/fixturegen/run.sh

# or from PowerShell
scripts/fixturegen/run.ps1
```

Either script builds the image, runs the container with
`internal/laz/testdata/las` mounted read-only at `/in` and a staging dir
(`scripts/fixturegen/out`, not committed) at `/out`, then copies the outputs
into the testdata tree. Output is fully deterministic (fixed LCG seed, fixed
header dates), so regeneration produces byte-identical files.

The harness **self-verifies everything** inside the container and exits
nonzero on any failure: every generated `.laz` is decompressed again with
`LASunzipper` and compared byte-for-byte against the corresponding `.las`
records.

## What it generates

### Group A — v1 re-compressions (→ `internal/laz/testdata/las/`)

Re-compressions of the existing
`las12_pf3_1000pts_with_extrabytes.las` (LAS 1.2, PF3, 42-byte records =
POINT10 + GPSTIME11 + RGB12 + 8 extra bytes, 1000 points). The original
header and VLRs are copied verbatim and patched (compressed bit, +1 VLR,
adjusted offset), the LASzip VLR payload comes from `LASzip::pack()`, and the
point data is compressed by `LASzipper`.

| file | compression |
|---|---|
| `las12_pf3_1000pts_with_extrabytes_v1.laz` | POINTWISE_CHUNKED (compressor 2), chunk_size 100, `request_version(1)` → all items v1 |
| `las12_pf3_1000pts_with_extrabytes_v1.las` | byte-identical copy of the input .las (pairing twin for the e2e sweep) |
| `las12_pf3_1000pts_with_extrabytes_v1pw.laz` | POINTWISE (compressor 1, non-chunked: no chunk-table offset slot, no chunk table), v1 items |
| `las12_pf3_1000pts_with_extrabytes_v1pw.las` | byte-identical copy of the input .las |

### Group B — LAS 1.4 multichannel fixtures (→ `internal/laz/testdata/cpporacle/`)

1000 new deterministic points (LCG seed 20260703) whose **scanner_channel
switches between 0..3 in blocks of 3–10 points** (160 switches; 127 of them
return to a channel already used within the same 100-point chunk) — this
exercises the per-context (multichannel) state of the v3/v4 codecs. Each
channel has distinct value regimes (RGB/intensity/classification/user_data
bases) so per-context state diverges; scan angles include deltas > 16 and
±2000 jumps; gps_time is increasing with occasional +10 s jumps.

| fixture triple | contents |
|---|---|
| `las14_pf7_v3_multichannel_1000pts.{las,laz,csv}` | LAS 1.4, PF7 (POINT14+RGB14), LAYERED_CHUNKED (compressor 3), chunk_size 100, default v3 items |
| `las14_pf8_v4_multichannel_1000pts.{las,laz,csv}` | LAS 1.4, PF8 (POINT14+RGBNIR14) + 8 extra bytes (GridID uint32, Confidence float32, BYTE14 item), `request_version(4)` → v4 items, chunk_size 100 |

The `.las` twins are written by the genuine LASzip **raw** item writers
(`LASzipper` with compressor NONE), so `.las` and `.laz` contain identical
logical points. The `.csv` oracle (26 columns, x/y/z scaled by 0.001,
gps_time `%.17g`, wavepacket/x_t/y_t/z_t columns 0, GridID/Confidence 0 for
PF7) is populated **by decoding the `.laz` with `LASunzipper`** — the C++
decoder is the oracle — not from the generation arrays.

### Group C — corrupt variants (→ `internal/laz/testdata/cpporacle/corrupt/`)

Byte-surgery on the existing `las12_pf3_1000pts_with_extrabytes.laz`
(POINTWISE_CHUNKED v2, chunk_size 100, offset_to_point_data 864, 28924
bytes):

| file | corruption |
|---|---|
| `chunked_tableoffset_minus1.laz` | 8-byte chunk-table offset slot at offset_to_point_data overwritten with -1 (0xFF×8) |
| `chunked_tableoffset_garbage.laz` | same slot overwritten with offset_to_point_data+500 (points into compressed data) |
| `chunked_truncated.laz` | file truncated to offset_to_point_data + 60 % of the point-data bytes (cut mid-chunk 7 of 10; chunk table gone) |

`oracle.json` records what the real C++ `LASreadPoint` does with each file
(`open_ok`, `points_ok` = points decoded without error, `error_at` = 0-based
index of the first failing read or null, `matches_las` = whether all decoded
points byte-match the pristine `.las`, plus the reader's `error`/`warning`
strings). Observed behavior: LASzip recovers from both corrupt chunk-table
offsets by falling back to sequential chunk reading (all 1000 points decode
correctly, only a warning is set); the truncated file decodes the 600 points
of the 6 intact chunks correctly and then fails with "end-of-file during
chunk with index 6".

### Group D — mid-chunk corruption salvage oracle (→ `internal/laz/testdata/cpporacle/corrupt/`)

`chunked_midchunk_corrupt.laz`: the pristine
`las12_pf3_1000pts_with_extrabytes.laz` with the 4-byte arithmetic-decoder
seed of chunk 3 (points 300–399) overwritten (file offset = chunk-3 start +
42, right after the chunk's raw first point). The 8-byte table-offset slot
and the chunk table remain intact and valid (10 tabled chunks — the harness
decodes the chunk table with the real `ArithmeticDecoder` +
`IntegerCompressor` primitives to locate chunk 3). The harness iterates a
fixed candidate list until the C++ `LASreadPoint::read()` demonstrably
THROWS mid-chunk ("chunk with index 3 ... is corrupt") rather than only
tripping the chunk-boundary position check; the first candidate (seed →
0xFFFFFFFF) already does.

`oracle_salvage.json` records the corruption (offsets + original/corrupt
bytes) and the observed behavior of ALL 1000 `read()` calls, where reading
CONTINUES after failures because C++ recovers by seeking to the next tabled
chunk start. Segment kinds:
- `{"reads_from", "reads_to", "pristine_from"}` — successful reads whose
  records byte-match the pristine `.las` records starting at `pristine_from`
  (contiguous; every read is byte-verified),
- `{"error_at_read", "error"}` — a single failing read,
- `{"reads_from", "reads_to", "failed": true, "error"}` — a run of failing
  reads with the same error,
- `{"reads_from", "reads_to", "garbage": true}` — successful reads matching
  no pristine record (none occurred with the chosen corruption).

Observed: reads 0–300 decode pristine points 0–300 (read 300 is chunk 3's
raw first point, stored before the corrupted seed); read 301 fails with
"chunk with index 3 of 11 is corrupt"; reads 302–901 salvage pristine points
400–999; reads 902–999 fail with "end-of-file during chunk with index 10"
(the reader walks into the chunk table after the last real chunk).

### Group E — LAS 1.4 compatibility-mode fixtures (→ `internal/laz/testdata/cpporacle/compat/`)

Written with the genuine DLL API (`laszip_create` → header setup →
`laszip_request_compatibility_mode(…, 1)` → `laszip_open_writer(…,
compress=TRUE)` → 1000 × `laszip_write_point`): LAS 1.4 point clouds recoded
by LASzip into legacy point types + "LAS 1.4 …" extra-byte attributes plus a
`lascompatible`/22204 marker VLR. 1000 deterministic points (LCG seed
20260704) exercise every field the compat recoding touches: scanner_channel
0–3 in blocks of 3–10 (148 switches), the FULL 4-bit return range
(returns/counts up to 15; 512 points with number_of_returns > 7),
classifications above 31 (308 points), 16-bit scan angles with large deltas
and ±3000 jumps, and per-channel RGB+NIR regimes for PF8.

| fixture | contents |
|---|---|
| `las14_pf6_compat_1000pts.laz` | written as PF6/reclen 30 → on disk LAS 1.2, PF 1\|0x80, reclen 33 (28 + 5 compat extra bytes), chunked v2 compression |
| `las14_pf8_compat_1000pts.laz` | written as PF8/reclen 38 → on disk LAS 1.2, PF 3\|0x80, reclen 41 (34 + 5 + 2 NIR extra bytes) |
| `las14_pf6_compat_1000pts.las` / `las14_pf8_compat_1000pts.las` | the SAME points written as NATIVE uncompressed PF6/PF8 LAS 1.4 (compress=FALSE, no compat mode) |
| `las14_pf6_compat_1000pts.csv` / `las14_pf8_compat_1000pts.csv` | 26-column oracle dumped from the DLL READER's read-back of the .laz (the reader detects the `lascompatible` VLR — reconstruction must be requested via `laszip_request_compatibility_mode` on the reader too — and rebuilds the PF6/PF8 points); GridID/Confidence are 0 (no user extra bytes) |
| `compat_oracle.json` | per .laz: raw on-disk header values + VLR list + extra-byte descriptor names + compat marker VLR id, and what the DLL reader reports after `laszip_open_reader` (PF 6/8, reclen 30/38, version 1.4, zero VLRs/attributes visible) |

Self-verify: the DLL read-back of each compat `.laz` is compared per point
(all extended fields, gps_time, RGB/NIR) against the DLL read of the native
`.las` AND against the generated arrays; any mismatch aborts.

## Files

- `Dockerfile` — debian:bookworm-slim, clones LASzip at the pinned commit,
  compiles the harness against all of `LASzip/src/*.cpp` including
  `laszip_dll.cpp` (statically; `-DLASZIPDLL_EXPORTS` makes `lasindex.cpp`
  include `lasreadpoint.hpp` instead of the LASlib-only `lasreader.hpp`,
  exactly like the upstream DLL build). The deprecated-but-ideal
  `laszipper.cpp`/`lasunzipper.cpp` binding needs the `IS_LITTLE_ENDIAN()`
  macro, which is defined on the compile line in terms of
  `Endian::IS_LITTLE_ENDIAN` from `mydefs.hpp`.
- `genfixtures.cpp` — the harness (heavily commented; see the notes on the
  in-memory POINT14 item layout shared by the raw and v3/v4 codecs).
- `run.sh` / `run.ps1` — build + run + copy into testdata.
