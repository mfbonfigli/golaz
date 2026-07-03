# LAZ Writer Porting Plan (LASzip C++ → golaz)

> **Status (2026-07-03): COMPLETE — all four phases are done.**
>
> - [x] **Phase 1 (foundations):** ByteStreamOut streams, ArithmeticEncoder, IntegerCompressor compress side — byte-exact vs the decoder round-trips.
> - [x] **Phase 2 (pf 0–5):** raw + v2 item writers, LASwritePoint, chunk table, `LASzip.Pack()` — byte-exact vs the C++ `.laz` goldens.
> - [x] **Phase 3 (pf 6–10):** v3/v4 layered writers incl. multi-scanner-channel semantics — byte-exact vs the cpporacle goldens.
> - [x] **Phase 4 (public API):** `writer.go` (`Create`/`NewWriter`/`WritePoint`/`Chunk`/`SetCoordinates`/`Close`), header + VLR serialization, LASzip VLR emission, inventory patch-back on close, point setters + `NewPoint` — public-level byte-exactness proven in `writer_test.go`.
>
> Still deferred (out of scope for v1): v1 item writers (except WAVEPACKET13, which is
> always item-version 1 and IS supported — it unlocks pf 4/5/9/10 output), compatibility-mode
> output, POINTWISE (non-chunked) writing, EVLR writing, LAX index.

Plan for porting the write/compression side of LASzip to Go, based on a deep dive of the
C++ writer sources (`arithmeticencoder.cpp`, `integercompressor.cpp`, `laswritepoint.cpp`,
`laswriteitemcompressed_v1..v4.cpp`, `laszipper.cpp`, `laszip.cpp`, `laszip_dll.cpp` writer path,
`bytestreamout*.hpp`). File:line anchors below refer to the LASzip repo (`LZ`) and this repo (`GO`).

## 1. Scope

**In scope (target: functional writer with parity for what modern LASzip emits by default):**
- Uncompressed LAS writing (pf 0–10) — the `compress=false` path, nearly free.
- POINTWISE_CHUNKED (compressor 2) with **v2 item writers**: POINT10, GPSTIME11, RGB12, BYTE — the C++ default for pf 0–5.
- LAYERED_CHUNKED (compressor 3) with **v3 item writers**: POINT14, RGB14, RGBNIR14, BYTE14 — the C++ default for pf 6–10 / LAS 1.4.
- **v4 item writers** (default only for LAS 1.5) — ~6 changed lines vs v3, include cheaply via a flag.
- Fixed-size chunking (default 50 000) + chunk table (seekable and non-seekable stream paths).
- Variable-size chunks (`chunk_size == U32_MAX`, explicit `Chunk()` API) — small plumbing, reader already supports it; include.
- `LASzip.Pack()` (inverse of existing `Unpack`), LASzip VLR emission, LAS header/VLR writing,
  optional inventory patch-back on close (point counts, per-return counts, bbox).

**Out of scope (defer):**
- v1 item writers (nothing emits v1 unless explicitly requested; readers exist for legacy files only).
- Compatibility mode (1.4→1.3 rewiring + "lascompatible" VLR, LZ\laszip_dll.cpp:2096–2379).
- POINTWISE non-chunked (compressor 1) writing.
- WAVEPACKET13_v1 / WAVEPACKET14_v3/v4 writers (pf 4/5/9/10) — defer unless waveform output is a goal.
- LAX spatial index, special EVLRs (always write -1/-1 in the VLR like the C++ dll writer).

## 2. Key formats and mechanics (from the C++ deep dive)

### 2.1 Write lifecycle
`open → write*N → (chunk boundaries) → done/close`:
1. `LASwritePoint::init` (LZ\laswritepoint.cpp:257): first call reserves the 8-byte chunk-table
   offset slot at `offset_to_point_data`. Seekable: record `chunk_table_start_position = tell()`
   and write **that value itself** as the placeholder (load-bearing: the reader's
   "interrupted compressor" detection checks `chunk_table_start + 8 == chunks_start`).
   Non-seekable: write `-1` and append the real table position after the table at the end.
2. `write(point)` (LZ\laswritepoint.cpp:296): at `chunk_count == chunk_size`, close the chunk
   (layered: U32 point count + per-item `chunk_sizes()` + per-item `chunk_bytes()`; non-layered:
   `enc.done()`), record `tell() - chunk_start_position` via `add_chunk_to_table`, re-init.
3. **First point of every chunk is written raw** to the main stream; then each compressed
   writer's `Init(item, &context)` seeds `last_item`/models (POINT14 sets `context`, downstream
   items consume it), then `enc.init(outstream)` — raw bytes precede the encoder init.
4. `done()` (LZ\laswritepoint.cpp:392): flush partial chunk, add to table, `write_chunk_table()`,
   patch the offset slot (seekable path).

### 2.2 Chunk table format (LZ\laswritepoint.cpp:459–507)
```
chunk_table_offset → U32 LE version (=0)
                     U32 LE number_chunks
                     [if >0] fresh enc.init; IntegerCompressor(enc, 32, 2) initCompressor;
                       per chunk i:
                         variable mode only: ic.compress(prev_count, count[i], ctx 0)
                         always:             ic.compress(prev_bytes, bytes[i], ctx 1)
                       enc.done()
                     [non-seekable only] I64 LE chunk_table_offset
```
Values are **per-chunk raw counts/bytes predicted by the previous chunk's value** — NOT
cumulative; the reader accumulates them (GO\lasreadpoint.go:458–462). Easy to get backwards.

### 2.3 v3/v4 layered write mechanics
- POINT14 v3 writes 9 layers (`channel_returns_XY, Z, classification, flags, intensity,
  scan_angle, user_data, point_source, gps_time`), each with its own `ArithmeticEncoder` bound
  to its own in-memory out-stream. RGB14: 1 layer; RGBNIR14: 2; BYTE14: 1 per extra byte.
- `chunk_sizes()`: `done()` on the XY and Z encoders unconditionally, on the other 7 only if
  their `changed_*` flag is set; then write 9 × U32 sizes to the **main** stream (0 for
  unchanged layers; XY and Z always real). `chunk_bytes()` then writes changed layers' buffers
  in the same order. Sizes are sampled **after** `done()` (the 2–3 tail bytes count).
- Unchanged-layer dropping is lossless because the chunk's raw first point carries the value.
- **v3 vs v4 (verified by diff, must be version-gated):**
  - POINT14: v3 sets the shared `context` only inside the scanner-channel-changed branch
    (LZ\laswriteitemcompressed_v3.cpp:576-578); v4 sets it unconditionally (v4:576).
  - RGB/RGBNIR/WAVEPACKET/BYTE: v3 rebinds `last_item` to the new context **only when that
    context was unused** (stale-last_item behavior, v3:1285–1293); v4 rebinds unconditionally.
  - The Go readers replicate both behaviors per version; the writers must too, or
    multi-scanner-channel files will be corrupt. (NOTE: the v3 *reader* currently has a bug
    here — see the reader review; fix it before using it as the round-trip oracle.)

### 2.4 Encoder specifics (vs the ported decoder)
- State: `base`/`length` + output buffer. `init` does NOT preload 4 bytes (the decoder's seed
  comes from the encoder's flush).
- Carry detection: `init_base > base` after `uint32` wraparound addition — ports directly.
- `encodeSymbol` shift asymmetry (LZ\arithmeticencoder.cpp:228–237): last-symbol case uses
  `length >> shift` non-destructively; other case destructively `length >>= shift` before BOTH
  products. Same embedded-`>>=` pattern in writeBits/writeByte/writeShort. Get this exact.
- `writeBits`: >19 bits → low `writeShort` first, then upper `bits-16`. writeInt = low short
  then high short; writeInt64 = low int then high int (mirrors decoder).
- `done()` flush: if `length > 2*AC__MinLength` → `base += AC__MinLength; length = AC__MinLength>>1`
  (one more renorm byte); else `base += AC__MinLength>>1; length = AC__MinLength>>9` (two).
  Then carry + renorm, then **2 zero bytes, plus a 3rd if the first branch was taken**.
  These tail bytes are part of every layer size and chunk byte count.
- **Do not port the 2×4096 ring buffer.** Buffer the entire init→done production in a growing
  `[]byte`, do carry propagation directly on the slice (walk backwards, 0xFF→0x00, increment
  first non-0xFF), flush in `done()`. Byte output is identical; the C++ ring was a bug source.
- Models are shared with the decoder; add a `compress bool` so encoder-side models skip the
  decoder-table build (LZ\arithmeticmodel.cpp:104,155) — memory only, distributions identical.

### 2.5 IntegerCompressor compress side (LZ\integercompressor.cpp:219–273, 364–465)
Constructor/corr-range math identical to the existing decompressor. `compress(pred, real, ctx)`:
`corr = real - pred` (int32 wrap), fold by ±corr_range, then `writeCorrector`:
`k = bitlen(c<=0 ? -c : c-1)`; encode k via mBits[ctx]; k==0 → encodeBit; 0<k<32 → translate c
to [0,2^k) (neg: `c += (1<<k)-1`; pos: `c -= 1`), single symbol if `k <= bits_high` else
high-bits symbol + `writeBits(k-bits_high)` low bits; k==32 → nothing more (reader returns corr_min).
Store k for `GetK()` — the dx→dy→z context chaining depends on compress-then-getK statement order.

## 3. Components in dependency order

| # | Component | File(s) | Est. LOC | Notes |
|---|---|---|---|---|
| 1 | `ByteStreamOut` interface | GO\internal\laz\bytestreamout.go (new) | ~80 | PutByte/PutBytes/Put16/32/64LE, Tell/Seek/SeekEnd/IsSeekable |
| 2 | In-memory array out-stream | bytestreamout_array.go (new) | ~120 | For v3/v4 layers; reset (`buf = buf[:0]`) per chunk instead of C++ `seek(0)`+`getCurr` reuse |
| 3 | File / io.WriteSeeker / io.Writer out-streams | bytestreamout_file.go, bytestreamout_writer.go (new) | ~150 | LE only |
| 4 | Encoder-mode flag in models | arithmeticmodel.go (modify) | ~15 | `compress` flag skips decoder-table build |
| 5 | `ArithmeticEncoder` | arithmeticencoder.go (new) | ~300 | Slice-buffered output, carry-on-slice, exact done() tail |
| 6 | IC compress side | integercompressor.go (extend) | ~130 | InitCompressor/Compress/writeCorrector; reuse corr-range math |
| 7 | Writer item interfaces | writeitem.go (new) | ~60 | Write(item, ctx), Init(item, ctx), ChunkSizes(), ChunkBytes() |
| 8 | Raw item writers | writeitem_raw.go (new) | ~220 | POINT14 must invert the 40-byte temp layout of readitem_raw.go:96–230 |
| 9 | v2 compressed writers | writeitem_compressed_v2.go (new) | ~600 | Mirrors readitem_compressed_v2.go; reuses StreamingMedian5, return maps, u8Fold |
| 10 | v3 compressed writers | writeitem_compressed_v3.go (new) | ~1600–1900 | Context structs in common_v3.go reusable once IC is dual-mode |
| 11 | v4 compressed writers | flag in #10 or thin v4 file | ~50 | Only the context-switch/last_item rebinding differences |
| 12 | `LASwritePoint` | laswritepoint.go (new) | ~400 | Chunking, chunk table build/patch, Chunk(), Done() |
| 13 | `LASzip.Pack()` + writer defaults | laszip_config.go (extend) | ~50 | Inverse of Unpack (34+6n bytes); SetupByPointType/RequestVersion/SetChunkSize already exist |
| 14 | Public `Writer` API | writer.go + header/vlr serialization (new) | ~700–900 | Header writing incl. the three compress-mode patches (offset_to_point_data += 54+34+6n; nVLRs+1; format\|128); optional inventory patch-back on Close |
| 15 | Tests | *_test.go | ~800 | See §4 |

Total: ~4,500–5,500 new LOC plus tests — comparable to the reader side (~5,900 LOC).

Suggested phasing:
- **Phase 1 (foundations):** #1–#6 + unit round-trip tests (encoder↔decoder, IC compress↔decompress
  across k=0 / k≤8 / k>8 / k=32 branches).
- **Phase 2 (pf 0–5):** #7–#9, #12, #13 minimal, chunk-table round-trip test against the existing
  `readChunkTable`, e2e write→read for pf 0–3 testdata.
- **Phase 3 (pf 6–10):** #10–#11, layered chunk round-trips, multi-scanner-channel test cases.
- **Phase 4 (public API):** #14, header/VLR writing, inventory, byte-exact comparison vs C++ output.

## 4. Testing strategy

1. **Property round-trips** (Phase 1): random symbol/bit/writeBits sequences encoded with fresh
   models and decoded with fresh models; IC compress↔decompress over all corrector branches.
2. **Reader-as-oracle e2e**: for every file in internal/laz/testdata — read with the existing
   (trusted, e2e-tested) reader → write with the new writer (same pf, chunk size 50 000, same
   item versions) → re-read → field-by-field compare. Exercises every item writer.
3. **Byte-exact goldens**: the pipeline is deterministic — compressed point-data stream + chunk
   table for identical input/config should match C++ LASzip byte-for-byte from
   `offset_to_point_data` onward (header/VLR cosmetic fields will differ). Existing testdata
   .laz files can serve as goldens if their chunk size / item versions are matched.
4. **Targeted cases**: chunk boundary exactly at file end (`writers == 0` at done()),
   partial final chunk, variable-size chunks with explicit `Chunk()`, non-seekable output,
   multi-scanner-channel pf6 files (v3 vs v4 semantics), unchanged-layer dropping
   (constant classification/intensity chunks), extra-bytes (BYTE14 per-byte layers).

## 5. Risk register (tricky spots)

1. `encodeSymbol` / `writeBits` destructive-shift asymmetry — streams decode fine for a while
   then diverge if wrong.
2. Carry propagation reach — solved structurally by the slice buffer.
3. `done()` 2-vs-3 zero tail bytes — miscounting breaks the reader's chunk-boundary integrity
   check (`chunkStarts[i] != tell()`).
4. Layer size sampling order (after done(); 0 for unchanged; XY/Z always real) and 9-layer order.
5. v3 vs v4 context-switch semantics must be gated on the item version being written.
6. Signed wraparound: do arithmetic in `int32`, not `int` (`corr = real - pred`, gpstime
   `multi * last_gpstime_diff`, `-c` at INT32_MIN).
7. Float-dependent control flow must be `float32`: gpstime `multi = I32_QUANTIZE(f32(curr)/f32(last))`
   (quantize = ±0.5 then truncate), POINT14 raw `I16_QUANTIZE(scan_angle_rank/0.006f)`.
8. `GetK()` ordering: ic_dx.Compress before its k feeds ic_dy's context, etc. — follow the
   reader's statement order.
9. Chunk table values are per-chunk deltas predicted by the previous chunk, not cumulative.
10. Chunk-table placeholder on seekable streams = the slot's own file position, not 0/-1.
11. First-point-raw-then-enc.Init ordering, including POINT14 → downstream `context` seeding.
12. Header patches (offset+54+payload, nVLRs+1, format|128) apply to bytes on disk only.

## 6. Pre-existing reader work that unblocks the writer

- ~~Fix the v3 POINT14 `*context` propagation bug~~ DONE (2026-07-03): the assignment now
  lives inside the scanner-channel-changed branch, verified against C++-generated
  multichannel fixtures (internal/laz/testdata/cpporacle/).
- ~~Fix `bitsHigh` 4→8 in readitem_compressed_v1.go~~ DONE (2026-07-03): verified against
  C++-generated v1-item fixtures (testdata/las/*_v1.laz).
- The config layer (SetupByPointType, RequestVersion, SetChunkSize, checkItems) is already
  writer-ready; only `Pack()` is missing.
- The Dockerized C++ harness in scripts/fixturegen/ can be extended to generate golden
  outputs for writer byte-exactness tests (same pinned LASzip commit).
