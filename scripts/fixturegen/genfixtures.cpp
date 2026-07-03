/*
===============================================================================

  genfixtures.cpp -- golaz test-fixture generator harness

  Compiled against the REAL LASzip C++ sources (upstream commit
  ac3e9e9f954427872c8c77b68b7766cf706322f9) so that every .laz fixture is
  produced by the reference implementation, and so that the behavior of the
  reference reader on corrupt files can be recorded as an oracle.

  Usage:
      genfixtures <input.las> <input.laz> <outdir>

  where
      <input.las>  = las12_pf3_1000pts_with_extrabytes.las  (LAS 1.2, PF3,
                     42-byte records = 34 base + 8 extra bytes, 1000 points)
      <input.laz>  = las12_pf3_1000pts_with_extrabytes.laz  (the existing
                     POINTWISE_CHUNKED v2 compression of the same points,
                     chunk_size 100 -- used as the base for the corrupt
                     Group C fixtures)
      <outdir>     = output root; files are written to
                       <outdir>/las/...            (Group A)
                       <outdir>/cpporacle/...      (Group B)
                       <outdir>/cpporacle/corrupt/ (Group C + oracle.json)

  Groups:
    A: re-compressions of the input .las with item version 1
       (POINTWISE_CHUNKED chunk_size 100, and plain POINTWISE non-chunked),
       each self-verified by decompressing with LASunzipper and comparing
       every record byte-for-byte with the input .las.
    B: new multichannel LAS 1.4 fixtures (PF7 v3 and PF8+extrabytes v4,
       LAYERED_CHUNKED chunk_size 100) with scanner_channel switching between
       0..3 in small blocks, plus .las twins written by the LASzip raw
       writers and .csv oracles populated by DECODING the .laz with
       LASunzipper.
    C: corrupt variants of the input .laz (chunk table offset slot smashed,
       or file truncated mid-chunk) plus oracle.json recording what the real
       C++ LASreadPoint does with each file.
    D: a mid-chunk corruption of the input .laz (a few bytes inside the
       compressed payload of chunk 3, table offset slot and chunk table left
       intact) plus oracle_salvage.json recording, segment by segment, how
       the real C++ LASreadPoint fails inside chunk 3 and then SALVAGES the
       remaining chunks by seeking to the next tabled chunk start
       (-> <outdir>/cpporacle/corrupt/).
    E: LAS 1.4 "compatibility mode" fixtures written with the genuine DLL
       API (laszip_request_compatibility_mode + laszip_open_writer): PF6 and
       PF8 point clouds recoded into PF1/PF3 + "extra bytes", with native
       uncompressed pf6/pf8 .las twins, CSV oracles produced by the DLL
       READER (which reconstructs the LAS 1.4 points from the compatibility
       VLRs), and compat_oracle.json describing the on-disk vs reconstructed
       header (-> <outdir>/cpporacle/compat/).

  All self-verifications abort the program (nonzero exit) on any mismatch.

===============================================================================
*/

#include "laszip.hpp"
#include "laszipper.hpp"
#include "lasunzipper.hpp"
#include "lasreadpoint.hpp"
#include "bytestreamin_file.hpp"
#include "arithmeticdecoder.hpp"
#include "integercompressor.hpp"
#include "laszip_api.h" // the DLL API, compiled statically into this harness

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cstdint>
#include <cstdarg>
#include <string>
#include <vector>

#ifdef _WIN32
#include <direct.h>
#define MKDIR(p) _mkdir(p)
#else
#include <sys/stat.h>
#define MKDIR(p) mkdir(p, 0755)
#endif

// ----------------------------------------------------------------------------
// small helpers
// ----------------------------------------------------------------------------

static void die(const char* fmt, ...)
{
  va_list args;
  va_start(args, fmt);
  fprintf(stderr, "FATAL: ");
  vfprintf(stderr, fmt, args);
  fprintf(stderr, "\n");
  va_end(args);
  exit(1);
}

static std::vector<uint8_t> read_file(const std::string& path)
{
  FILE* f = fopen(path.c_str(), "rb");
  if (!f) die("cannot open %s for reading", path.c_str());
  fseek(f, 0, SEEK_END);
  long size = ftell(f);
  fseek(f, 0, SEEK_SET);
  std::vector<uint8_t> data((size_t)size);
  if (size && fread(data.data(), 1, (size_t)size, f) != (size_t)size)
    die("short read on %s", path.c_str());
  fclose(f);
  return data;
}

static void write_file(const std::string& path, const std::vector<uint8_t>& data)
{
  FILE* f = fopen(path.c_str(), "wb");
  if (!f) die("cannot open %s for writing", path.c_str());
  if (data.size() && fwrite(data.data(), 1, data.size(), f) != data.size())
    die("short write on %s", path.c_str());
  fclose(f);
}

static void make_dir(const std::string& path) { MKDIR(path.c_str()); }

// little-endian scalar access into byte buffers
static uint16_t rd_u16(const uint8_t* p) { uint16_t v; memcpy(&v, p, 2); return v; }
static uint32_t rd_u32(const uint8_t* p) { uint32_t v; memcpy(&v, p, 4); return v; }
static int16_t  rd_i16(const uint8_t* p) { int16_t v; memcpy(&v, p, 2); return v; }
static double   rd_f64(const uint8_t* p) { double v; memcpy(&v, p, 8); return v; }
static float    rd_f32(const uint8_t* p) { float v; memcpy(&v, p, 4); return v; }
static void wr_u16(uint8_t* p, uint16_t v) { memcpy(p, &v, 2); }
static void wr_u32(uint8_t* p, uint32_t v) { memcpy(p, &v, 4); }
static void wr_u64(uint8_t* p, uint64_t v) { memcpy(p, &v, 8); }
static void wr_i16(uint8_t* p, int16_t v)  { memcpy(p, &v, 2); }
static void wr_i32(uint8_t* p, int32_t v)  { memcpy(p, &v, 4); }
static void wr_f64(uint8_t* p, double v)   { memcpy(p, &v, 8); }
static void wr_f32(uint8_t* p, float v)    { memcpy(p, &v, 4); }

// deterministic 64-bit LCG (do NOT use rand(): must be reproducible everywhere)
struct Lcg
{
  uint64_t state;
  explicit Lcg(uint64_t seed) : state(seed) {}
  uint32_t next()
  {
    state = state * 6364136223846793005ULL + 1442695040888963407ULL;
    return (uint32_t)(state >> 33);
  }
  uint32_t next(uint32_t n) { return next() % n; } // n > 0
};

// ----------------------------------------------------------------------------
// LAS parsing helpers
// ----------------------------------------------------------------------------

struct LasFile
{
  std::vector<uint8_t> bytes;
  uint16_t header_size;
  uint32_t offset_to_point_data;
  uint32_t num_vlrs;
  uint8_t  point_data_format;   // high bit stripped
  bool     compressed;          // high bit of the format byte
  uint16_t record_length;
  uint32_t num_points;          // legacy count (all our inputs are LAS 1.2)

  const uint8_t* record(uint32_t i) const
  {
    return bytes.data() + offset_to_point_data + (size_t)i * record_length;
  }
};

static LasFile parse_las(const std::string& path)
{
  LasFile f;
  f.bytes = read_file(path);
  if (f.bytes.size() < 227 || memcmp(f.bytes.data(), "LASF", 4) != 0)
    die("%s is not a LAS file", path.c_str());
  f.header_size = rd_u16(&f.bytes[94]);
  f.offset_to_point_data = rd_u32(&f.bytes[96]);
  f.num_vlrs = rd_u32(&f.bytes[100]);
  f.point_data_format = f.bytes[104] & 0x7f;
  f.compressed = (f.bytes[104] & 0x80) != 0;
  f.record_length = rd_u16(&f.bytes[105]);
  f.num_points = rd_u32(&f.bytes[107]);
  return f;
}

// locate the payload of the LASzip VLR ("laszip encoded", record id 22204)
static std::vector<uint8_t> find_laszip_vlr(const LasFile& f)
{
  size_t pos = f.header_size;
  for (uint32_t i = 0; i < f.num_vlrs; i++)
  {
    if (pos + 54 > f.bytes.size()) die("VLR overruns file");
    const uint8_t* vlr = f.bytes.data() + pos;
    uint16_t record_id = rd_u16(vlr + 18);
    uint16_t payload_len = rd_u16(vlr + 20);
    if (memcmp(vlr + 2, "laszip encoded", 14) == 0 && record_id == 22204)
      return std::vector<uint8_t>(vlr + 54, vlr + 54 + payload_len);
    pos += 54 + payload_len;
  }
  die("no LASzip VLR found");
  return {};
}

// build a 54-byte VLR header + payload
static std::vector<uint8_t> build_vlr(const char* user_id, uint16_t record_id,
                                      const char* description,
                                      const std::vector<uint8_t>& payload)
{
  std::vector<uint8_t> v(54 + payload.size(), 0);
  // reserved = 0
  strncpy((char*)&v[2], user_id, 16);
  wr_u16(&v[18], record_id);
  wr_u16(&v[20], (uint16_t)payload.size());
  strncpy((char*)&v[22], description, 32);
  memcpy(&v[54], payload.data(), payload.size());
  return v;
}

// summary of a LASzip VLR payload (for logging / report)
static void print_laszip_vlr_summary(const std::string& path)
{
  LasFile f = parse_las(path);
  std::vector<uint8_t> payload = find_laszip_vlr(f);
  LASzip laszip;
  if (!laszip.unpack(payload.data(), (int)payload.size()))
    die("cannot unpack LASzip VLR of %s: %s", path.c_str(), laszip.get_error());
  static const char* names[] = { "BYTE", "SHORT", "INT", "LONG", "FLOAT", "DOUBLE",
    "POINT10", "GPSTIME11", "RGB12", "WAVEPACKET13", "POINT14", "RGB14",
    "RGBNIR14", "WAVEPACKET14", "BYTE14" };
  printf("  VLR summary %s: compressor=%u coder=%u version=%u.%ur%u chunk_size=%u items=[",
         path.c_str(), laszip.compressor, laszip.coder, laszip.version_major,
         laszip.version_minor, laszip.version_revision, laszip.chunk_size);
  for (int i = 0; i < laszip.num_items; i++)
  {
    printf("%s%s(size=%u,v%u)", i ? ", " : "", names[laszip.items[i].type],
           laszip.items[i].size, laszip.items[i].version);
  }
  printf("]\n");
}

// ----------------------------------------------------------------------------
// GROUP A -- re-compressions of the pf3 input with v1 items
// ----------------------------------------------------------------------------

// The pf3 record is 42 bytes: POINT10 (20) + GPSTIME11 (8) + RGB12 (6) + BYTE (8).
// For these item types the in-memory item layout used by LASzipper/LASunzipper
// is identical to the on-disk layout, so item pointers can point straight into
// the raw 42-byte record.

static void item_ptrs_from_record(uint8_t* rec, const LASzip& laszip,
                                  std::vector<uint8_t*>& ptrs)
{
  ptrs.clear();
  size_t off = 0;
  for (int i = 0; i < laszip.num_items; i++)
  {
    ptrs.push_back(rec + off);
    off += laszip.items[i].size;
  }
}

// verify that a Group-A style .laz decodes byte-for-byte to the .las records
static void verify_laz_group_a(const std::string& laz_path, const LasFile& las)
{
  LasFile lazf = parse_las(laz_path);
  if (!lazf.compressed) die("%s: not compressed?", laz_path.c_str());
  std::vector<uint8_t> payload = find_laszip_vlr(lazf);
  LASzip laszip;
  if (!laszip.unpack(payload.data(), (int)payload.size()))
    die("%s: unpack failed: %s", laz_path.c_str(), laszip.get_error());

  FILE* fp = fopen(laz_path.c_str(), "rb");
  if (!fp) die("cannot reopen %s", laz_path.c_str());
  fseek(fp, (long)lazf.offset_to_point_data, SEEK_SET);

  LASunzipper unzipper;
  if (!unzipper.open(fp, &laszip))
    die("%s: LASunzipper.open failed: %s", laz_path.c_str(), unzipper.get_error());

  std::vector<uint8_t> rec(las.record_length);
  std::vector<uint8_t*> ptrs;
  item_ptrs_from_record(rec.data(), laszip, ptrs);

  for (uint32_t i = 0; i < las.num_points; i++)
  {
    if (!unzipper.read(ptrs.data()))
      die("%s: read failed at point %u: %s", laz_path.c_str(), i, unzipper.get_error());
    if (memcmp(rec.data(), las.record(i), las.record_length) != 0)
      die("%s: BYTE MISMATCH at point %u", laz_path.c_str(), i);
  }
  unzipper.close();
  fclose(fp);
  printf("  verify %s: 1000/1000 records byte-identical to input .las\n", laz_path.c_str());
}

// write a .laz re-compression of the input .las:
//   copy original header + original VLRs verbatim, patch the header fields,
//   append the LASzip VLR, then compress all records with LASzipper.
static void write_laz_group_a(const LasFile& las, uint16_t compressor,
                              uint16_t requested_version, int chunk_size,
                              const std::string& out_path)
{
  LASzip laszip;
  if (!laszip.setup(las.point_data_format, las.record_length, compressor))
    die("laszip.setup failed: %s", laszip.get_error());
  if (!laszip.request_version(requested_version))
    die("laszip.request_version failed: %s", laszip.get_error());
  if (chunk_size > 0)
  {
    if (!laszip.set_chunk_size((unsigned)chunk_size))
      die("laszip.set_chunk_size failed: %s", laszip.get_error());
  }

  unsigned char* vlr_payload_ptr;
  int vlr_payload_len;
  if (!laszip.pack(vlr_payload_ptr, vlr_payload_len))
    die("laszip.pack failed: %s", laszip.get_error());
  std::vector<uint8_t> vlr_payload(vlr_payload_ptr, vlr_payload_ptr + vlr_payload_len);

  // header + original VLRs, patched
  std::vector<uint8_t> head(las.bytes.begin(), las.bytes.begin() + las.offset_to_point_data);
  head[104] |= 0x80;                                            // mark compressed
  wr_u32(&head[100], las.num_vlrs + 1);                         // one extra VLR
  wr_u32(&head[96], las.offset_to_point_data + 54 + (uint32_t)vlr_payload.size());

  std::vector<uint8_t> laszip_vlr =
      build_vlr("laszip encoded", 22204, "by golaz fixturegen (genuine LASzip)", vlr_payload);
  head.insert(head.end(), laszip_vlr.begin(), laszip_vlr.end());

  FILE* fp = fopen(out_path.c_str(), "wb");
  if (!fp) die("cannot open %s for writing", out_path.c_str());
  if (fwrite(head.data(), 1, head.size(), fp) != head.size())
    die("short write on %s", out_path.c_str());

  LASzipper zipper;
  if (!zipper.open(fp, &laszip))
    die("%s: LASzipper.open failed: %s", out_path.c_str(), zipper.get_error());

  std::vector<uint8_t> rec(las.record_length);
  std::vector<uint8_t*> ptrs;
  item_ptrs_from_record(rec.data(), laszip, ptrs);

  for (uint32_t i = 0; i < las.num_points; i++)
  {
    memcpy(rec.data(), las.record(i), las.record_length);
    if (!zipper.write(ptrs.data()))
      die("%s: write failed at point %u: %s", out_path.c_str(), i, zipper.get_error());
  }
  if (!zipper.close())
    die("%s: LASzipper.close failed: %s", out_path.c_str(), zipper.get_error());
  fclose(fp);

  printf("wrote %s\n", out_path.c_str());
  verify_laz_group_a(out_path, las);
  print_laszip_vlr_summary(out_path);
}

// additional structural check for the pointwise fixture: the compressed point
// data must start IMMEDIATELY at offset_to_point_data (no 8-byte chunk table
// offset slot, hence no chunk table). With pointwise coding the first point
// is stored raw, so its first 20 bytes must equal the first POINT10 of the
// input .las.
static void check_pointwise_no_chunk_table(const std::string& laz_path, const LasFile& las)
{
  LasFile lazf = parse_las(laz_path);
  std::vector<uint8_t> payload = find_laszip_vlr(lazf);
  if (rd_u16(&payload[0]) != 1) die("%s: compressor field != 1", laz_path.c_str());
  if (memcmp(lazf.bytes.data() + lazf.offset_to_point_data, las.record(0), 20) != 0)
    die("%s: point data does not start with the raw first point -- an unexpected "
        "chunk table offset slot seems to be present", laz_path.c_str());
  printf("  %s: compressor=1 (POINTWISE) confirmed; point data begins directly with "
         "the raw first point => no chunk table offset slot / no chunk table\n",
         laz_path.c_str());
}

// ----------------------------------------------------------------------------
// GROUP B -- multichannel LAS 1.4 fixtures (PF7 v3 / PF8 v4)
// ----------------------------------------------------------------------------

// Logical point attributes we generate.
struct GenPoint
{
  int32_t X, Y, Z;
  uint16_t intensity;
  uint8_t return_number;       // 1..5
  uint8_t number_of_returns;   // 1..5
  uint8_t scan_direction_flag; // 0/1
  uint8_t edge_of_flight_line; // 0/1
  uint8_t classification;      // 2 + channel
  uint8_t user_data;
  uint16_t point_source_id;
  int16_t scan_angle;
  uint8_t scanner_channel;     // 0..3
  double gps_time;
  uint16_t red, green, blue, nir;
  uint32_t grid_id;            // extra bytes (pf8 only)
  float confidence;            // extra bytes (pf8 only)
};

// The in-memory POINT14 item layout expected by both
//   - LASwriteItemRaw_POINT14_LE   (see laswriteitemraw.hpp, LAStempWritePoint10)
//   - LASwriteItemCompressed_POINT14_v3/_v4 (see the LASpoint14 struct at the
//     top of laswriteitemcompressed_v3.cpp)
// Both structs agree on every byte offset that either writer actually reads:
//   [0]  I32 X   [4] I32 Y   [8] I32 Z   [12] U16 intensity
//   [14] legacy ret:3 | legacy num:3 | scan_direction_flag:1 | edge:1
//   [15] legacy classification:5 | legacy flags:3
//   [16] I8 legacy scan angle rank    [17] U8 user_data
//   [18] U16 point_source_ID          [20] I16 scan_angle (extended)
//   [22] point_type:2 | scanner_channel:2 | classification_flags:4
//   [23] U8 classification (extended) [24] return_number:4 | number_of_returns:4
//   [25..27] internal                 [28] BOOL gps_time_change (internal)
//   [32] F64 gps_time
// The v3/v4 codecs memcpy sizeof(LASpoint14) == 48 bytes from/to the item
// pointer, so the buffer we hand over must be at least 48 bytes. We use 64.
static const size_t P14_MEM_SIZE = 64;

static void fill_point14_mem(uint8_t* m, const GenPoint& p)
{
  memset(m, 0, P14_MEM_SIZE);
  wr_i32(m + 0, p.X);
  wr_i32(m + 4, p.Y);
  wr_i32(m + 8, p.Z);
  wr_u16(m + 12, p.intensity);
  uint8_t lret = p.return_number > 7 ? 7 : p.return_number;
  uint8_t lnum = p.number_of_returns > 7 ? 7 : p.number_of_returns;
  m[14] = (uint8_t)((lret & 7) | ((lnum & 7) << 3) |
                    ((p.scan_direction_flag & 1) << 6) |
                    ((p.edge_of_flight_line & 1) << 7));
  m[15] = (uint8_t)(p.classification & 31);   // legacy classification, flags 0
  // legacy scan angle rank (informational only; never hits the disk for PF>5)
  double sar = 0.006 * p.scan_angle;
  int isar = (int)(sar < 0 ? sar - 0.5 : sar + 0.5);
  if (isar < -128) isar = -128; if (isar > 127) isar = 127;
  m[16] = (uint8_t)(int8_t)isar;
  m[17] = p.user_data;
  wr_u16(m + 18, p.point_source_id);
  wr_i16(m + 20, p.scan_angle);
  m[22] = (uint8_t)(1 |                              /* extended point type */
                    ((p.scanner_channel & 3) << 2) |
                    (0 << 4));                       /* classification flags = 0 */
  m[23] = p.classification;
  m[24] = (uint8_t)((p.return_number & 0xF) | ((p.number_of_returns & 0xF) << 4));
  wr_f64(m + 32, p.gps_time);
}

// Convert the in-memory POINT14 item (as produced by LASunzipper -- either by
// the raw readers or the v3/v4 codecs) to the 30-byte LAS 1.4 on-disk format.
// This mirrors the "extended" branch of LASwriteItemRaw_POINT14_LE.
static void point14_mem_to_disk(const uint8_t* m, uint8_t out[30])
{
  memcpy(out, m, 14);                                       // X, Y, Z, intensity
  uint8_t ret = m[24] & 0x0F;
  uint8_t num = (m[24] >> 4) & 0x0F;
  out[14] = (uint8_t)(ret | (num << 4));
  uint8_t class_flags = (m[22] >> 4) & 0x0F;
  uint8_t channel = (m[22] >> 2) & 0x03;
  uint8_t sdf = (m[14] >> 6) & 1;
  uint8_t edge = (m[14] >> 7) & 1;
  out[15] = (uint8_t)(class_flags | (channel << 4) | (sdf << 6) | (edge << 7));
  out[16] = m[23];                                          // classification
  out[17] = m[17];                                          // user_data
  memcpy(out + 18, m + 20, 2);                              // scan_angle
  memcpy(out + 20, m + 18, 2);                              // point_source_ID
  memcpy(out + 22, m + 32, 8);                              // gps_time
}

// generate the deterministic multichannel point cloud
static std::vector<GenPoint> generate_points(uint32_t n, int& channel_switches,
                                             int& intra_chunk_revisits)
{
  Lcg lcg(20260703ULL); // fixed seed
  std::vector<GenPoint> pts(n);

  int32_t X = 100000, Y = 200000, Z = 50000;
  double gps = 400000.0;
  int16_t chan_angle[4] = { -900, -300, 300, 900 };
  uint8_t channel = 0;
  uint32_t block_left = 0;
  channel_switches = 0;
  intra_chunk_revisits = 0;
  bool used_in_chunk[4] = { false, false, false, false };

  for (uint32_t i = 0; i < n; i++)
  {
    if (i % 100 == 0) // new 100-point chunk: reset the per-chunk usage map
    {
      for (int c = 0; c < 4; c++) used_in_chunk[c] = false;
      if (i) used_in_chunk[channel] = false;
    }
    if (block_left == 0)
    {
      block_left = 3 + lcg.next(8); // blocks of 3..10 points
      if (i)
      {
        uint8_t next_chan = (uint8_t)lcg.next(4);
        if (next_chan == channel) next_chan = (uint8_t)((next_chan + 1) & 3);
        channel = next_chan;
        channel_switches++;
        if (used_in_chunk[channel]) intra_chunk_revisits++;
      }
    }
    used_in_chunk[channel] = true;
    block_left--;

    GenPoint& p = pts[i];
    p.scanner_channel = channel;

    // XYZ random walk
    X += (int32_t)lcg.next(2001) - 1000;
    Y += (int32_t)lcg.next(2001) - 1000;
    Z += (int32_t)lcg.next(401) - 200;
    p.X = X; p.Y = Y; p.Z = Z;

    // per-channel value regimes so per-context state diverges
    p.intensity = (uint16_t)(channel * 8000 + lcg.next(2000));
    p.classification = (uint8_t)(2 + channel);
    p.user_data = (uint8_t)(10 + channel * 40 + lcg.next(10));
    p.point_source_id = (uint16_t)(1000 + channel * 17 + lcg.next(3));
    p.red   = (uint16_t)(channel * 12000 + lcg.next(4096));
    p.green = (uint16_t)(channel * 12000 + 2048 + lcg.next(4096));
    p.blue  = (uint16_t)(channel * 12000 + 4096 + lcg.next(4096));
    p.nir   = (uint16_t)(channel * 9000 + lcg.next(4096));

    // returns
    p.number_of_returns = (uint8_t)(1 + lcg.next(5));
    p.return_number = (uint8_t)(1 + lcg.next(p.number_of_returns));
    p.scan_direction_flag = (uint8_t)lcg.next(2);
    p.edge_of_flight_line = (uint8_t)(lcg.next(50) == 0 ? 1 : 0);

    // scan angle: per-channel base, walk with deltas up to +-30 and
    // occasional jumps of +-2000 (so many deltas exceed 16)
    int16_t& base = chan_angle[channel];
    base = (int16_t)(base + (int32_t)lcg.next(61) - 30);
    if (lcg.next(40) == 0) base = (int16_t)(base + ((int32_t)lcg.next(2) ? 2000 : -2000));
    p.scan_angle = base;

    // gps time: increasing with occasional jumps
    gps += 0.0001 * (1 + lcg.next(10));
    if (lcg.next(100) == 0) gps += 10.0;
    p.gps_time = gps;

    // extra bytes (used for pf8 only)
    p.grid_id = 100000 + i * 3 + lcg.next(3);
    float conf = (float)lcg.next(1000) / 1000.0f;
    p.confidence = conf;
  }
  return pts;
}

// build the 375-byte LAS 1.4 header
static std::vector<uint8_t> build_las14_header(const std::vector<GenPoint>& pts,
                                               uint8_t pdf, uint16_t reclen,
                                               uint32_t nvlr, uint32_t offset,
                                               bool compressed)
{
  std::vector<uint8_t> h(375, 0);
  memcpy(&h[0], "LASF", 4);
  // file source id (4), global encoding (6) = 0 (matches the existing corpus)
  h[24] = 1; h[25] = 4;                              // version 1.4
  strncpy((char*)&h[26], "golaz fixturegen", 32);    // system identifier
  strncpy((char*)&h[58], "LASzip ac3e9e9 harness", 32); // generating software
  wr_u16(&h[90], 184);                               // day of year (fixed)
  wr_u16(&h[92], 2026);                              // year (fixed)
  wr_u16(&h[94], 375);                               // header size
  wr_u32(&h[96], offset);
  wr_u32(&h[100], nvlr);
  h[104] = (uint8_t)(pdf | (compressed ? 0x80 : 0));
  wr_u16(&h[105], reclen);
  // legacy point count + legacy by-return: MUST be zero for PF >= 6
  wr_f64(&h[131], 0.001); wr_f64(&h[139], 0.001); wr_f64(&h[147], 0.001); // scales
  // offsets (155..178) stay 0
  double minx = 1e300, maxx = -1e300, miny = 1e300, maxy = -1e300, minz = 1e300, maxz = -1e300;
  uint64_t by_return[15] = { 0 };
  for (const GenPoint& p : pts)
  {
    double x = p.X * 0.001, y = p.Y * 0.001, z = p.Z * 0.001;
    if (x < minx) minx = x; if (x > maxx) maxx = x;
    if (y < miny) miny = y; if (y > maxy) maxy = y;
    if (z < minz) minz = z; if (z > maxz) maxz = z;
    if (p.return_number >= 1 && p.return_number <= 15) by_return[p.return_number - 1]++;
  }
  wr_f64(&h[179], maxx); wr_f64(&h[187], minx);
  wr_f64(&h[195], maxy); wr_f64(&h[203], miny);
  wr_f64(&h[211], maxz); wr_f64(&h[219], minz);
  // start of waveform (227) = 0, start of first EVLR (235) = 0, number of EVLRs (243) = 0
  wr_u64(&h[247], pts.size());                       // extended point count
  for (int r = 0; r < 15; r++) wr_u64(&h[255 + 8 * r], by_return[r]);
  return h;
}

// build the Extra Bytes VLR (LASF_Spec / record 4) describing
// GridID (uint32) + Confidence (float32)
static std::vector<uint8_t> build_extra_bytes_vlr()
{
  std::vector<uint8_t> payload(2 * 192, 0);
  // descriptor 0: GridID, data_type 5 (unsigned long = uint32), options 0
  payload[2] = 5;
  strncpy((char*)&payload[4], "GridID", 32);
  strncpy((char*)&payload[160], "grid cell id", 32);
  // descriptor 1: Confidence, data_type 9 (float), options 0
  payload[192 + 2] = 9;
  strncpy((char*)&payload[192 + 4], "Confidence", 32);
  strncpy((char*)&payload[192 + 160], "confidence 0..1", 32);
  return build_vlr("LASF_Spec", 4, "Extra Bytes Record", payload);
}

struct GroupBFiles
{
  std::string las, laz, csv;
};

// write .las (raw writers) + .laz (LAYERED_CHUNKED) + .csv (decoded from .laz)
static void write_group_b(const std::vector<GenPoint>& pts, uint8_t pdf,
                          uint16_t requested_version, const GroupBFiles& out)
{
  const bool pf8 = (pdf == 8);
  const uint16_t reclen = pf8 ? (uint16_t)46 : (uint16_t)36; // 30+8+8 / 30+6
  const uint32_t n = (uint32_t)pts.size();

  // ---- VLRs shared by .las and .laz (extra bytes descriptor for pf8) ----
  std::vector<uint8_t> eb_vlr;
  if (pf8) eb_vlr = build_extra_bytes_vlr();

  // ---- prepare per-point item buffers ----
  // POINT14 in-memory item (>= 48 bytes), RGB / RGBNIR item, BYTE14 item
  std::vector<uint8_t> p14(P14_MEM_SIZE), rgb(8), eb(8);

  auto fill_items = [&](const GenPoint& p) {
    fill_point14_mem(p14.data(), p);
    wr_u16(&rgb[0], p.red);
    wr_u16(&rgb[2], p.green);
    wr_u16(&rgb[4], p.blue);
    if (pf8) wr_u16(&rgb[6], p.nir);
    if (pf8) { wr_u32(&eb[0], p.grid_id); wr_f32(&eb[4], p.confidence); }
  };

  // ---------------- write the .las via the REAL raw writers ----------------
  {
    LASzip laszip;
    if (!laszip.setup(pdf, reclen, LASZIP_COMPRESSOR_NONE))
      die("las setup failed: %s", laszip.get_error());

    uint32_t nvlr = pf8 ? 1 : 0;
    uint32_t offset = 375 + (uint32_t)eb_vlr.size();
    std::vector<uint8_t> head = build_las14_header(pts, pdf, reclen, nvlr, offset, false);
    head.insert(head.end(), eb_vlr.begin(), eb_vlr.end());

    FILE* fp = fopen(out.las.c_str(), "wb");
    if (!fp) die("cannot open %s", out.las.c_str());
    fwrite(head.data(), 1, head.size(), fp);

    LASzipper zipper; // with compressor NONE this drives the raw item writers
    if (!zipper.open(fp, &laszip))
      die("%s: LASzipper.open failed: %s", out.las.c_str(), zipper.get_error());

    uint8_t* ptrs[3] = { p14.data(), rgb.data(), eb.data() };
    for (uint32_t i = 0; i < n; i++)
    {
      fill_items(pts[i]);
      if (!zipper.write(ptrs))
        die("%s: write failed at %u: %s", out.las.c_str(), i, zipper.get_error());
    }
    if (!zipper.close()) die("%s: close failed", out.las.c_str());
    fclose(fp);
    printf("wrote %s\n", out.las.c_str());
  }

  // ---------------- write the .laz (LAYERED_CHUNKED, chunk 100) ----------------
  {
    LASzip laszip;
    if (!laszip.setup(pdf, reclen, LASZIP_COMPRESSOR_LAYERED_CHUNKED))
      die("laz setup failed: %s", laszip.get_error());
    if (!laszip.request_version(requested_version))
      die("laz request_version failed: %s", laszip.get_error());
    if (!laszip.set_chunk_size(100))
      die("laz set_chunk_size failed: %s", laszip.get_error());

    unsigned char* vlr_payload_ptr;
    int vlr_payload_len;
    if (!laszip.pack(vlr_payload_ptr, vlr_payload_len))
      die("laz pack failed: %s", laszip.get_error());
    std::vector<uint8_t> vlr_payload(vlr_payload_ptr, vlr_payload_ptr + vlr_payload_len);
    std::vector<uint8_t> laszip_vlr =
        build_vlr("laszip encoded", 22204, "by golaz fixturegen (genuine LASzip)", vlr_payload);

    uint32_t nvlr = (pf8 ? 1 : 0) + 1;
    uint32_t offset = 375 + (uint32_t)eb_vlr.size() + (uint32_t)laszip_vlr.size();
    std::vector<uint8_t> head = build_las14_header(pts, pdf, reclen, nvlr, offset, true);
    head.insert(head.end(), eb_vlr.begin(), eb_vlr.end());
    head.insert(head.end(), laszip_vlr.begin(), laszip_vlr.end());

    FILE* fp = fopen(out.laz.c_str(), "wb");
    if (!fp) die("cannot open %s", out.laz.c_str());
    fwrite(head.data(), 1, head.size(), fp);

    LASzipper zipper;
    if (!zipper.open(fp, &laszip))
      die("%s: LASzipper.open failed: %s", out.laz.c_str(), zipper.get_error());

    uint8_t* ptrs[3] = { p14.data(), rgb.data(), eb.data() };
    for (uint32_t i = 0; i < n; i++)
    {
      fill_items(pts[i]);
      if (!zipper.write(ptrs))
        die("%s: write failed at %u: %s", out.laz.c_str(), i, zipper.get_error());
    }
    if (!zipper.close()) die("%s: close failed", out.laz.c_str());
    fclose(fp);
    printf("wrote %s\n", out.laz.c_str());
  }

  // ------- decode the .laz with LASunzipper (the C++ oracle), verify -------
  // byte-for-byte against the .las records, and emit the CSV from the
  // DECODED values.
  {
    LasFile lasf = parse_las(out.las);
    LasFile lazf = parse_las(out.laz);
    std::vector<uint8_t> payload = find_laszip_vlr(lazf);
    LASzip laszip;
    if (!laszip.unpack(payload.data(), (int)payload.size()))
      die("%s: unpack failed: %s", out.laz.c_str(), laszip.get_error());

    FILE* fp = fopen(out.laz.c_str(), "rb");
    if (!fp) die("cannot reopen %s", out.laz.c_str());
    fseek(fp, (long)lazf.offset_to_point_data, SEEK_SET);
    LASunzipper unzipper;
    if (!unzipper.open(fp, &laszip))
      die("%s: LASunzipper.open failed: %s", out.laz.c_str(), unzipper.get_error());

    FILE* csv = fopen(out.csv.c_str(), "wb");
    if (!csv) die("cannot open %s", out.csv.c_str());
    fprintf(csv, "x,y,z,intensity,return_number,number_of_returns,scan_direction_flag,"
                 "edge_of_flight_line,classification,user_data,point_source_id,scan_angle,"
                 "gps_time,red,green,blue,nir,wavepacket_index,wavepacket_offset,"
                 "wavepacket_size,return_point_wave_location,x_t,y_t,z_t,GridID,Confidence\n");

    std::vector<uint8_t> dp14(P14_MEM_SIZE, 0), drgb(8, 0), deb(8, 0);
    uint8_t* ptrs[3] = { dp14.data(), drgb.data(), deb.data() };
    uint8_t disk[46];

    for (uint32_t i = 0; i < n; i++)
    {
      if (!unzipper.read(ptrs))
        die("%s: decode failed at %u: %s", out.laz.c_str(), i, unzipper.get_error());

      // re-serialize the decoded point to the LAS 1.4 on-disk record format
      point14_mem_to_disk(dp14.data(), disk);
      if (pf8)
      {
        memcpy(disk + 30, drgb.data(), 8); // RGBNIR14: disk == memory
        memcpy(disk + 38, deb.data(), 8);  // BYTE14:   disk == memory
      }
      else
      {
        memcpy(disk + 30, drgb.data(), 6); // RGB14: disk == memory
      }
      if (memcmp(disk, lasf.record(i), reclen) != 0)
        die("%s: decoded point %u does not byte-match the .las record", out.laz.c_str(), i);

      // CSV row (from the DECODED in-memory representation)
      double x = (int32_t)rd_u32(&dp14[0]) * 0.001;
      double y = (int32_t)rd_u32(&dp14[4]) * 0.001;
      double z = (int32_t)rd_u32(&dp14[8]) * 0.001;
      uint16_t intensity = rd_u16(&dp14[12]);
      uint8_t ret = dp14[24] & 0x0F, num = (dp14[24] >> 4) & 0x0F;
      uint8_t sdf = (dp14[14] >> 6) & 1, edge = (dp14[14] >> 7) & 1;
      uint8_t cls = dp14[23], ud = dp14[17];
      uint16_t psid = rd_u16(&dp14[18]);
      int16_t sa = rd_i16(&dp14[20]);
      double gps = rd_f64(&dp14[32]);
      uint16_t r = rd_u16(&drgb[0]), g = rd_u16(&drgb[2]), b = rd_u16(&drgb[4]);
      uint16_t nir = pf8 ? rd_u16(&drgb[6]) : 0;
      uint32_t grid = pf8 ? rd_u32(&deb[0]) : 0;
      float conf = pf8 ? rd_f32(&deb[4]) : 0.0f;

      fprintf(csv, "%.3f,%.3f,%.3f,%u,%u,%u,%u,%u,%u,%u,%u,%d,%.17g,%u,%u,%u,%u,"
                   "0,0,0,0,0,0,0,%u,%.9g\n",
              x, y, z, intensity, ret, num, sdf, edge, cls, ud, psid, (int)sa, gps,
              r, g, b, nir, grid, (double)conf);
    }
    unzipper.close();
    fclose(fp);
    fclose(csv);
    printf("  verify %s: 1000/1000 decoded records byte-identical to %s\n",
           out.laz.c_str(), out.las.c_str());
    printf("wrote %s (populated by LASunzipper decode)\n", out.csv.c_str());
    print_laszip_vlr_summary(out.laz);
  }
}

// ----------------------------------------------------------------------------
// GROUP C -- corrupt chunked .laz variants + C++ oracle
// ----------------------------------------------------------------------------

struct OracleResult
{
  bool open_ok = false;
  uint32_t points_ok = 0;    // number of points decoded without error
  int error_at = -1;         // 0-based index of the first failing read, -1 = none
  bool matches_las = true;   // decoded points byte-match the pristine .las
  std::string error_msg;     // LASreadPoint::error() at failure (if any)
  std::string warning_msg;   // LASreadPoint::warning() (e.g. chunk table recovery)
};

// run the genuine C++ LASreadPoint over a (corrupt) .laz and record behavior
static OracleResult run_oracle(const std::string& laz_path, const LasFile& pristine)
{
  OracleResult res;

  LasFile lazf = parse_las(laz_path);
  std::vector<uint8_t> payload = find_laszip_vlr(lazf);
  LASzip laszip;
  if (!laszip.unpack(payload.data(), (int)payload.size()))
  {
    res.error_msg = laszip.get_error() ? laszip.get_error() : "unpack failed";
    return res;
  }

  FILE* fp = fopen(laz_path.c_str(), "rb");
  if (!fp) die("cannot open %s", laz_path.c_str());
  fseek(fp, (long)lazf.offset_to_point_data, SEEK_SET);
  ByteStreamIn* stream = new ByteStreamInFileLE(fp);

  LASreadPoint reader;
  if (!reader.setup(laszip.num_items, laszip.items, &laszip) || !reader.init(stream))
  {
    res.open_ok = false;
    res.error_msg = reader.error() ? reader.error() : "setup/init failed";
    delete stream;
    fclose(fp);
    return res;
  }
  res.open_ok = true;

  std::vector<uint8_t> rec(pristine.record_length);
  std::vector<uint8_t*> ptrs;
  size_t off = 0;
  for (int i = 0; i < laszip.num_items; i++)
  {
    ptrs.push_back(rec.data() + off);
    off += laszip.items[i].size;
  }

  for (uint32_t i = 0; i < pristine.num_points; i++)
  {
    if (!reader.read(ptrs.data()))
    {
      res.error_at = (int)i;
      res.error_msg = reader.error() ? reader.error() : "read failed";
      break;
    }
    res.points_ok++;
    if (memcmp(rec.data(), pristine.record(i), pristine.record_length) != 0)
      res.matches_las = false;
  }
  if (reader.warning()) res.warning_msg = reader.warning();
  reader.done();
  delete stream;
  fclose(fp);
  return res;
}

static std::string json_escape(const std::string& s)
{
  std::string out;
  for (char c : s)
  {
    if (c == '"' || c == '\\') { out += '\\'; out += c; }
    else if (c == '\n') out += "\\n";
    else out += c;
  }
  return out;
}

static void append_oracle_json(std::string& json, const std::string& name,
                               const OracleResult& r, bool last)
{
  char buf[1024];
  snprintf(buf, sizeof(buf),
           "  \"%s\": {\"open_ok\": %s, \"points_ok\": %u, \"error_at\": %s, "
           "\"matches_las\": %s, \"error\": %s, \"warning\": %s}%s\n",
           name.c_str(), r.open_ok ? "true" : "false", r.points_ok,
           r.error_at < 0 ? "null" : std::to_string(r.error_at).c_str(),
           r.matches_las ? "true" : "false",
           r.error_msg.empty() ? "null" : ("\"" + json_escape(r.error_msg) + "\"").c_str(),
           r.warning_msg.empty() ? "null" : ("\"" + json_escape(r.warning_msg) + "\"").c_str(),
           last ? "" : ",");
  json += buf;
}

// ----------------------------------------------------------------------------
// GROUP D -- mid-chunk corruption + salvage oracle
// ----------------------------------------------------------------------------

// Parse the chunk table of a POINTWISE_CHUNKED .laz using the real LASzip
// primitives (mirrors LASreadPoint::read_chunk_table). Returns the absolute
// file offset of every chunk start; chunk i covers points
// [i*chunk_size, (i+1)*chunk_size).
static std::vector<int64_t> parse_chunk_table(const std::string& laz_path)
{
  LasFile lazf = parse_las(laz_path);
  FILE* fp = fopen(laz_path.c_str(), "rb");
  if (!fp) die("cannot open %s", laz_path.c_str());
  fseek(fp, (long)lazf.offset_to_point_data, SEEK_SET);
  ByteStreamInFileLE stream(fp);

  int64_t table_pos;
  stream.get64bitsLE((uint8_t*)&table_pos);
  int64_t chunks_start = stream.tell();

  if (!stream.seek(table_pos)) die("%s: cannot seek to chunk table", laz_path.c_str());
  uint32_t version, number_chunks;
  stream.get32bitsLE((uint8_t*)&version);
  if (version != 0) die("%s: bad chunk table version %u", laz_path.c_str(), version);
  stream.get32bitsLE((uint8_t*)&number_chunks);

  std::vector<int64_t> starts(number_chunks + 1);
  starts[0] = chunks_start;
  ArithmeticDecoder dec;
  dec.init(&stream);
  IntegerCompressor ic(&dec, 32, 2);
  ic.initDecompressor();
  for (uint32_t i = 1; i <= number_chunks; i++)
    starts[i] = ic.decompress(i > 1 ? (uint32_t)starts[i - 1] : 0, 1);
  dec.done();
  for (uint32_t i = 1; i <= number_chunks; i++)
    starts[i] += starts[i - 1];
  fclose(fp);
  return starts; // starts[number_chunks] == start of the chunk table
}

// one read() call's outcome when driving LASreadPoint over a corrupt file
struct SalvageRead
{
  enum Kind { OK, GARBAGE, FAIL } kind;
  int pristine;      // pristine .las record index for OK reads (-1 otherwise)
  std::string error; // LASreadPoint::error() for FAIL reads
};

// Drive the C++ LASreadPoint over all n read() calls, CONTINUING after
// failures (LASzip recovers by seeking to the next tabled chunk start).
// Every successful read is byte-matched against the pristine .las records.
static std::vector<SalvageRead> run_salvage(const std::string& laz_path, const LasFile& pristine)
{
  LasFile lazf = parse_las(laz_path);
  std::vector<uint8_t> payload = find_laszip_vlr(lazf);
  LASzip laszip;
  if (!laszip.unpack(payload.data(), (int)payload.size()))
    die("%s: unpack failed: %s", laz_path.c_str(), laszip.get_error());

  FILE* fp = fopen(laz_path.c_str(), "rb");
  if (!fp) die("cannot open %s", laz_path.c_str());
  fseek(fp, (long)lazf.offset_to_point_data, SEEK_SET);
  ByteStreamIn* stream = new ByteStreamInFileLE(fp);

  LASreadPoint reader;
  if (!reader.setup(laszip.num_items, laszip.items, &laszip) || !reader.init(stream))
    die("%s: LASreadPoint setup/init failed", laz_path.c_str());

  std::vector<uint8_t> rec(pristine.record_length);
  std::vector<uint8_t*> ptrs;
  size_t off = 0;
  for (int i = 0; i < laszip.num_items; i++)
  {
    ptrs.push_back(rec.data() + off);
    off += laszip.items[i].size;
  }

  std::vector<SalvageRead> reads(pristine.num_points);
  for (uint32_t i = 0; i < pristine.num_points; i++)
  {
    if (reader.read(ptrs.data()))
    {
      // find the pristine record this read matches byte-for-byte (records are
      // unique, checked by the caller); no match => garbage read
      reads[i].kind = SalvageRead::GARBAGE;
      reads[i].pristine = -1;
      for (uint32_t p = 0; p < pristine.num_points; p++)
      {
        if (memcmp(rec.data(), pristine.record(p), pristine.record_length) == 0)
        {
          reads[i].kind = SalvageRead::OK;
          reads[i].pristine = (int)p;
          break;
        }
      }
    }
    else
    {
      reads[i].kind = SalvageRead::FAIL;
      reads[i].pristine = -1;
      reads[i].error = reader.error() ? reader.error() : "read failed";
    }
  }
  reader.done();
  delete stream;
  fclose(fp);
  return reads;
}

// collapse per-read outcomes into segments (ok runs with contiguous pristine
// mapping / garbage runs / failure runs with identical error strings)
struct SalvageSegment
{
  enum Kind { OK, GARBAGE, FAIL } kind;
  int reads_from, reads_to; // inclusive
  int pristine_from;        // OK only
  std::string error;        // FAIL only
};

static std::vector<SalvageSegment> segment_salvage(const std::vector<SalvageRead>& reads)
{
  std::vector<SalvageSegment> segs;
  size_t i = 0;
  while (i < reads.size())
  {
    SalvageSegment s;
    s.reads_from = (int)i;
    if (reads[i].kind == SalvageRead::FAIL)
    {
      s.kind = SalvageSegment::FAIL;
      s.error = reads[i].error;
      s.pristine_from = -1;
      while (i + 1 < reads.size() && reads[i + 1].kind == SalvageRead::FAIL &&
             reads[i + 1].error == s.error)
        i++;
    }
    else if (reads[i].kind == SalvageRead::OK)
    {
      s.kind = SalvageSegment::OK;
      s.pristine_from = reads[i].pristine;
      while (i + 1 < reads.size() && reads[i + 1].kind == SalvageRead::OK &&
             reads[i + 1].pristine == reads[i].pristine + 1)
        i++;
    }
    else
    {
      s.kind = SalvageSegment::GARBAGE;
      s.pristine_from = -1;
      while (i + 1 < reads.size() && reads[i + 1].kind == SalvageRead::GARBAGE)
        i++;
    }
    s.reads_to = (int)i;
    segs.push_back(s);
    i++;
  }
  return segs;
}

static std::string salvage_segments_json(const std::vector<SalvageSegment>& segs,
                                         const std::string& indent)
{
  std::string json;
  for (size_t k = 0; k < segs.size(); k++)
  {
    const SalvageSegment& s = segs[k];
    char buf[512];
    if (s.kind == SalvageSegment::OK)
    {
      snprintf(buf, sizeof(buf), "%s{\"reads_from\": %d, \"reads_to\": %d, \"pristine_from\": %d}",
               indent.c_str(), s.reads_from, s.reads_to, s.pristine_from);
    }
    else if (s.kind == SalvageSegment::GARBAGE)
    {
      snprintf(buf, sizeof(buf), "%s{\"reads_from\": %d, \"reads_to\": %d, \"garbage\": true}",
               indent.c_str(), s.reads_from, s.reads_to);
    }
    else if (s.reads_from == s.reads_to)
    {
      snprintf(buf, sizeof(buf), "%s{\"error_at_read\": %d, \"error\": \"%s\"}",
               indent.c_str(), s.reads_from, json_escape(s.error).c_str());
    }
    else
    {
      snprintf(buf, sizeof(buf), "%s{\"reads_from\": %d, \"reads_to\": %d, \"failed\": true, \"error\": \"%s\"}",
               indent.c_str(), s.reads_from, s.reads_to, json_escape(s.error).c_str());
    }
    json += buf;
    if (k + 1 < segs.size()) json += ",";
    json += "\n";
  }
  return json;
}

static std::string hex_string(const uint8_t* p, size_t n)
{
  std::string s;
  char b[4];
  for (size_t i = 0; i < n; i++) { snprintf(b, sizeof(b), "%02x", p[i]); s += b; }
  return s;
}

// Create chunked_midchunk_corrupt.laz + oracle_salvage.json. Iterates over a
// fixed list of corruption candidates (offsets relative to the start of chunk
// 3, i.e. points 300-399) until the observed C++ behavior is a THROW inside
// chunk 3 ("chunk with index 3 ... is corrupt") followed by successful
// salvage of chunks 4..9 -- not just the silent chunk-boundary mismatch.
static void write_group_d(const LasFile& base_laz, const LasFile& pristine,
                          const std::string& in_laz_path, const std::string& outdir)
{
  // pristine records must be unique for the byte-verified pristine mapping
  for (uint32_t a = 0; a < pristine.num_points; a++)
    for (uint32_t b = a + 1; b < pristine.num_points; b++)
      if (memcmp(pristine.record(a), pristine.record(b), pristine.record_length) == 0)
        die("pristine records %u and %u are identical; cannot build salvage oracle", a, b);

  std::vector<int64_t> starts = parse_chunk_table(in_laz_path);
  printf("chunk table of %s: %zu chunks\n", in_laz_path.c_str(), starts.size() - 1);
  if (starts.size() < 11) die("expected 10 chunks in the base .laz");
  const int64_t chunk3 = starts[3];
  const int64_t chunk4 = starts[4];
  printf("chunk 3 (points 300-399) occupies file bytes [%lld, %lld)\n",
         (long long)chunk3, (long long)chunk4);

  // candidate corruptions, tried in fixed (deterministic) order. The v2
  // pointwise-chunked chunk starts with the raw 42-byte first point followed
  // by the 4-byte arithmetic decoder seed -- smashing the seed or the first
  // compressed bytes tends to produce out-of-range symbols (throw 4711).
  struct Candidate { uint32_t rel; std::vector<uint8_t> bytes; };
  const std::vector<Candidate> candidates = {
    { 42, { 0xFF, 0xFF, 0xFF, 0xFF } },       // arithmetic decoder init value
    { 42, { 0x00, 0x00, 0x00, 0x00 } },
    { 46, { 0xFF, 0xFF, 0xFF, 0xFF } },       // first compressed bytes
    { 42, { 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF } },
    { 50, { 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF } },
  };

  const std::string out_path = outdir + "/cpporacle/corrupt/chunked_midchunk_corrupt.laz";
  bool found = false;
  Candidate chosen{0, {}};
  std::vector<uint8_t> original_bytes;
  std::vector<SalvageSegment> segs;

  for (const Candidate& cand : candidates)
  {
    std::vector<uint8_t> corrupted = base_laz.bytes;
    int64_t abs_off = chunk3 + cand.rel;
    if (abs_off + (int64_t)cand.bytes.size() >= chunk4)
      die("corruption candidate would leave chunk 3");
    std::vector<uint8_t> orig(corrupted.begin() + abs_off,
                              corrupted.begin() + abs_off + cand.bytes.size());
    if (orig == cand.bytes) continue; // no-op corruption, try next
    memcpy(&corrupted[abs_off], cand.bytes.data(), cand.bytes.size());
    write_file(out_path, corrupted);

    std::vector<SalvageRead> reads = run_salvage(out_path, pristine);
    std::vector<SalvageSegment> s = segment_salvage(reads);

    // evaluate: reads 0..299 all pristine 0..299, first failure inside chunk 3
    // with the mid-chunk "corrupt" error (not EOF, not a later chunk), and a
    // salvaged segment of 600 reads mapping to pristine 400..999
    bool head_ok = !s.empty() && s[0].kind == SalvageSegment::OK &&
                   s[0].reads_from == 0 && s[0].pristine_from == 0 && s[0].reads_to >= 299;
    int first_fail = -1;
    std::string fail_err;
    for (const SalvageSegment& seg : s)
      if (seg.kind == SalvageSegment::FAIL) { first_fail = seg.reads_from; fail_err = seg.error; break; }
    bool fail_ok = first_fail >= 300 && first_fail <= 399 &&
                   fail_err.find("chunk with index 3") != std::string::npos &&
                   fail_err.find("corrupt") != std::string::npos;
    bool salvage_ok = false;
    for (const SalvageSegment& seg : s)
      if (seg.kind == SalvageSegment::OK && seg.pristine_from == 400 &&
          (seg.reads_to - seg.reads_from) == 599 && seg.reads_from > first_fail)
        salvage_ok = true;
    printf("  candidate rel=%u bytes=%s: head_ok=%d first_fail=%d fail_ok=%d salvage_ok=%d (%s)\n",
           cand.rel, hex_string(cand.bytes.data(), cand.bytes.size()).c_str(),
           head_ok, first_fail, fail_ok, salvage_ok, fail_err.c_str());
    if (head_ok && fail_ok && salvage_ok)
    {
      found = true;
      chosen = cand;
      original_bytes = orig;
      segs = s;
      break;
    }
  }
  if (!found) die("no corruption candidate produced the required mid-chunk throw + salvage");

  // determinism sanity: run the oracle a second time and require identical segments
  {
    std::vector<SalvageSegment> again = segment_salvage(run_salvage(out_path, pristine));
    if (again.size() != segs.size()) die("salvage oracle is not deterministic");
    for (size_t k = 0; k < segs.size(); k++)
      if (again[k].kind != segs[k].kind || again[k].reads_from != segs[k].reads_from ||
          again[k].reads_to != segs[k].reads_to || again[k].pristine_from != segs[k].pristine_from ||
          again[k].error != segs[k].error)
        die("salvage oracle is not deterministic");
  }

  // write oracle_salvage.json
  std::string json = "{\n  \"chunked_midchunk_corrupt.laz\": {\n";
  char buf[512];
  snprintf(buf, sizeof(buf),
           "    \"corruption\": {\"chunk_index\": 3, \"chunk_start\": %lld, "
           "\"file_offset\": %lld, \"offset_in_chunk\": %u, "
           "\"original_bytes\": \"%s\", \"corrupt_bytes\": \"%s\"},\n",
           (long long)chunk3, (long long)(chunk3 + chosen.rel), chosen.rel,
           hex_string(original_bytes.data(), original_bytes.size()).c_str(),
           hex_string(chosen.bytes.data(), chosen.bytes.size()).c_str());
  json += buf;
  json += "    \"segments\": [\n";
  json += salvage_segments_json(segs, "      ");
  json += "    ]\n  }\n}\n";

  const std::string jpath = outdir + "/cpporacle/corrupt/oracle_salvage.json";
  FILE* jf = fopen(jpath.c_str(), "wb");
  if (!jf) die("cannot open %s", jpath.c_str());
  fwrite(json.data(), 1, json.size(), jf);
  fclose(jf);
  printf("wrote %s and %s\n", out_path.c_str(), jpath.c_str());
  printf("%s", json.c_str());
}

// ----------------------------------------------------------------------------
// GROUP E -- LAS 1.4 compatibility-mode fixtures via the DLL API
// ----------------------------------------------------------------------------

// logical point for the compatibility fixtures: exercises the FULL extended
// ranges that the compatibility recoding must squeeze into legacy fields
struct GenPointE
{
  int32_t X, Y, Z;
  uint16_t intensity;
  uint8_t return_number;        // 1..15 (full 4-bit range)
  uint8_t number_of_returns;    // 1..15
  uint8_t scan_direction_flag, edge_of_flight_line;
  uint8_t classification;       // includes values > 31
  uint8_t flags;                // 4-bit extended classification flags (incl. overlap bit 8)
  uint8_t user_data;
  uint16_t point_source_id;
  int16_t scan_angle;           // 16-bit extended scan angle, large deltas
  uint8_t scanner_channel;      // 0..3, switching in blocks of 3..10
  double gps_time;
  uint16_t red, green, blue, nir;
};

static std::vector<GenPointE> generate_points_e(uint32_t n)
{
  Lcg lcg(20260704ULL); // fixed seed (distinct from Group B)
  std::vector<GenPointE> pts(n);

  int32_t X = -50000, Y = 300000, Z = 20000;
  double gps = 500000.0;
  int16_t chan_angle[4] = { -12000, -4000, 4000, 12000 };
  uint8_t channel = 0;
  uint32_t block_left = 0;
  static const uint8_t flag_cycle[7] = { 0, 1, 2, 4, 8, 9, 12 };

  for (uint32_t i = 0; i < n; i++)
  {
    if (block_left == 0)
    {
      block_left = 3 + lcg.next(8); // blocks of 3..10 points
      if (i)
      {
        uint8_t next_chan = (uint8_t)lcg.next(4);
        if (next_chan == channel) next_chan = (uint8_t)((next_chan + 1) & 3);
        channel = next_chan;
      }
    }
    block_left--;

    GenPointE& p = pts[i];
    p.scanner_channel = channel;

    X += (int32_t)lcg.next(4001) - 2000;
    Y += (int32_t)lcg.next(4001) - 2000;
    Z += (int32_t)lcg.next(801) - 400;
    p.X = X; p.Y = Y; p.Z = Z;

    p.intensity = (uint16_t)(channel * 9000 + lcg.next(3000));
    // full 4-bit return range
    p.number_of_returns = (uint8_t)(1 + lcg.next(15));            // 1..15
    p.return_number = (uint8_t)(1 + lcg.next(p.number_of_returns)); // 1..nret
    p.scan_direction_flag = (uint8_t)lcg.next(2);
    p.edge_of_flight_line = (uint8_t)(lcg.next(40) == 0 ? 1 : 0);
    // classifications: mix of legacy-range (<32) and extended-only (>31)
    if (lcg.next(3) == 0)
      p.classification = (uint8_t)(40 + channel * 5 + lcg.next(4)); // 40..58 (> 31)
    else
      p.classification = (uint8_t)(2 + channel);                    // 2..5
    p.flags = flag_cycle[lcg.next(7)];
    p.user_data = (uint8_t)(20 + channel * 30 + lcg.next(8));
    p.point_source_id = (uint16_t)(2000 + channel * 23 + lcg.next(4));

    // 16-bit scan angle with large deltas and occasional huge jumps
    int16_t& base = chan_angle[channel];
    base = (int16_t)(base + (int32_t)lcg.next(201) - 100);
    if (lcg.next(30) == 0) base = (int16_t)(base + ((int32_t)lcg.next(2) ? 3000 : -3000));
    p.scan_angle = base;

    gps += 0.0002 * (1 + lcg.next(8));
    if (lcg.next(90) == 0) gps += 25.0;
    p.gps_time = gps;

    p.red   = (uint16_t)(channel * 11000 + lcg.next(4096));
    p.green = (uint16_t)(channel * 11000 + 1024 + lcg.next(4096));
    p.blue  = (uint16_t)(channel * 11000 + 2048 + lcg.next(4096));
    p.nir   = (uint16_t)(channel * 10000 + lcg.next(4096));
  }
  return pts;
}

static void dll_check(laszip_POINTER zip, laszip_I32 err, const char* what)
{
  if (err)
  {
    laszip_CHAR* msg = 0;
    laszip_get_error(zip, &msg);
    die("%s failed: %s", what, msg ? msg : "(no message)");
  }
}

// fill the DLL header struct for a LAS 1.4 pf6/pf8 file with our points
static void fill_dll_header(laszip_header* h, const std::vector<GenPointE>& pts,
                            uint8_t pdf, uint16_t reclen)
{
  h->file_source_ID = 0;
  h->global_encoding = 0;
  h->version_major = 1;
  h->version_minor = 4;
  memset(h->system_identifier, 0, 32);
  strncpy(h->system_identifier, "golaz fixturegen", 31);
  memset(h->generating_software, 0, 32);
  strncpy(h->generating_software, "LASzip ac3e9e9 harness", 31);
  h->file_creation_day = 184;
  h->file_creation_year = 2026;
  h->header_size = 375;
  h->offset_to_point_data = 375;
  h->number_of_variable_length_records = 0;
  h->point_data_format = pdf;
  h->point_data_record_length = reclen;
  h->number_of_point_records = 0; // legacy counters must be 0 for pf >= 6
  for (int i = 0; i < 5; i++) h->number_of_points_by_return[i] = 0;
  h->x_scale_factor = h->y_scale_factor = h->z_scale_factor = 0.001;
  h->x_offset = h->y_offset = h->z_offset = 0.0;
  double minx = 1e300, maxx = -1e300, miny = 1e300, maxy = -1e300, minz = 1e300, maxz = -1e300;
  uint64_t by_return[15] = { 0 };
  for (const GenPointE& p : pts)
  {
    double x = p.X * 0.001, y = p.Y * 0.001, z = p.Z * 0.001;
    if (x < minx) minx = x; if (x > maxx) maxx = x;
    if (y < miny) miny = y; if (y > maxy) maxy = y;
    if (z < minz) minz = z; if (z > maxz) maxz = z;
    if (p.return_number >= 1 && p.return_number <= 15) by_return[p.return_number - 1]++;
  }
  h->max_x = maxx; h->min_x = minx;
  h->max_y = maxy; h->min_y = miny;
  h->max_z = maxz; h->min_z = minz;
  h->start_of_waveform_data_packet_record = 0;
  h->start_of_first_extended_variable_length_record = 0;
  h->number_of_extended_variable_length_records = 0;
  h->extended_number_of_point_records = pts.size();
  for (int r = 0; r < 15; r++) h->extended_number_of_points_by_return[r] = by_return[r];
}

// fill the DLL point struct; satisfies the two laszip_write_point checks:
// legacy flags == extended flags & 7, and legacy classification == extended
// classification when <= 31 (0 otherwise)
static void fill_dll_point(laszip_point* p, const GenPointE& q, bool pf8)
{
  p->X = q.X; p->Y = q.Y; p->Z = q.Z;
  p->intensity = q.intensity;
  p->return_number = (q.return_number > 7 ? 7 : q.return_number) & 7;
  p->number_of_returns = (q.number_of_returns > 7 ? 7 : q.number_of_returns) & 7;
  p->scan_direction_flag = q.scan_direction_flag & 1;
  p->edge_of_flight_line = q.edge_of_flight_line & 1;
  p->classification = (q.classification <= 31) ? (q.classification & 31) : 0;
  p->synthetic_flag = q.flags & 1;
  p->keypoint_flag = (q.flags >> 1) & 1;
  p->withheld_flag = (q.flags >> 2) & 1;
  double sar = 0.006 * q.scan_angle;
  int isar = (int)(sar < 0 ? sar - 0.5 : sar + 0.5);
  if (isar < -128) isar = -128; if (isar > 127) isar = 127;
  p->scan_angle_rank = (laszip_I8)isar;
  p->user_data = q.user_data;
  p->point_source_ID = q.point_source_id;
  p->extended_scan_angle = q.scan_angle;
  p->extended_point_type = 1;
  p->extended_scanner_channel = q.scanner_channel & 3;
  p->extended_classification_flags = q.flags & 0xF;
  p->extended_classification = q.classification;
  p->extended_return_number = q.return_number & 0xF;
  p->extended_number_of_returns = q.number_of_returns & 0xF;
  p->gps_time = q.gps_time;
  p->rgb[0] = pf8 ? q.red : 0;
  p->rgb[1] = pf8 ? q.green : 0;
  p->rgb[2] = pf8 ? q.blue : 0;
  p->rgb[3] = pf8 ? q.nir : 0;
  memset(p->wave_packet, 0, 29);
}

// write one file (compressed compatibility-mode .laz, or native .las) with
// the genuine DLL API
static void dll_write_file(const std::vector<GenPointE>& pts, uint8_t pdf,
                           uint16_t reclen, bool compat_compressed,
                           const std::string& path)
{
  laszip_POINTER zip = 0;
  if (laszip_create(&zip)) die("laszip_create failed");

  laszip_header* h = 0;
  dll_check(zip, laszip_get_header_pointer(zip, &h), "laszip_get_header_pointer");
  fill_dll_header(h, pts, pdf, reclen);

  if (compat_compressed)
    dll_check(zip, laszip_request_compatibility_mode(zip, 1), "laszip_request_compatibility_mode");

  dll_check(zip, laszip_open_writer(zip, path.c_str(), compat_compressed ? 1 : 0),
            "laszip_open_writer");

  laszip_point* p = 0;
  dll_check(zip, laszip_get_point_pointer(zip, &p), "laszip_get_point_pointer");
  for (const GenPointE& q : pts)
  {
    fill_dll_point(p, q, pdf == 8);
    dll_check(zip, laszip_write_point(zip), "laszip_write_point");
  }
  dll_check(zip, laszip_close_writer(zip), "laszip_close_writer");
  laszip_destroy(zip);
  printf("wrote %s\n", path.c_str());
}

// the extended point fields as reconstructed / reported by the DLL reader
struct DllPointE
{
  int32_t X, Y, Z;
  uint16_t intensity;
  uint8_t ret, nret, sdf, edge, classification, flags, user_data, channel;
  uint16_t psid;
  int16_t scan_angle;
  double gps_time;
  uint16_t rgb[4];
};

struct DllHeaderInfo
{
  uint8_t point_data_format;
  uint16_t point_data_record_length;
  uint8_t version_minor;
  uint32_t num_vlrs;
  uint64_t npoints;
  int attributes_visible; // LASF_Spec record 4 descriptors still exposed
};

// read a file with the DLL reader (with compatibility-mode reconstruction
// requested) and return all points + what the reader reports for the header
static std::vector<DllPointE> dll_read_file(const std::string& path, uint32_t expect,
                                            DllHeaderInfo* info)
{
  laszip_POINTER zip = 0;
  if (laszip_create(&zip)) die("laszip_create failed");
  dll_check(zip, laszip_request_compatibility_mode(zip, 1), "laszip_request_compatibility_mode(reader)");
  laszip_BOOL is_compressed = 0;
  dll_check(zip, laszip_open_reader(zip, path.c_str(), &is_compressed), "laszip_open_reader");

  laszip_header* h = 0;
  dll_check(zip, laszip_get_header_pointer(zip, &h), "laszip_get_header_pointer");
  uint64_t npoints = h->number_of_point_records ? h->number_of_point_records
                                                : h->extended_number_of_point_records;
  if (info)
  {
    info->point_data_format = h->point_data_format;
    info->point_data_record_length = h->point_data_record_length;
    info->version_minor = h->version_minor;
    info->num_vlrs = h->number_of_variable_length_records;
    info->npoints = npoints;
    info->attributes_visible = 0;
    for (uint32_t i = 0; i < h->number_of_variable_length_records; i++)
      if (strncmp(h->vlrs[i].user_id, "LASF_Spec", 9) == 0 && h->vlrs[i].record_id == 4)
        info->attributes_visible = h->vlrs[i].record_length_after_header / 192;
  }
  if (npoints != expect) die("%s: expected %u points, header says %llu",
                             path.c_str(), expect, (unsigned long long)npoints);

  laszip_point* p = 0;
  dll_check(zip, laszip_get_point_pointer(zip, &p), "laszip_get_point_pointer");
  std::vector<DllPointE> out(expect);
  for (uint32_t i = 0; i < expect; i++)
  {
    dll_check(zip, laszip_read_point(zip), "laszip_read_point");
    DllPointE& d = out[i];
    d.X = p->X; d.Y = p->Y; d.Z = p->Z;
    d.intensity = p->intensity;
    d.ret = p->extended_return_number;
    d.nret = p->extended_number_of_returns;
    d.sdf = p->scan_direction_flag;
    d.edge = p->edge_of_flight_line;
    d.classification = p->extended_classification;
    d.flags = p->extended_classification_flags;
    d.user_data = p->user_data;
    d.channel = p->extended_scanner_channel;
    d.psid = p->point_source_ID;
    d.scan_angle = p->extended_scan_angle;
    d.gps_time = p->gps_time;
    for (int c = 0; c < 4; c++) d.rgb[c] = p->rgb[c];
  }
  dll_check(zip, laszip_close_reader(zip), "laszip_close_reader");
  laszip_destroy(zip);
  return out;
}

static void compare_dll_points(const std::vector<DllPointE>& a, const std::vector<DllPointE>& b,
                               const std::string& na, const std::string& nb)
{
  if (a.size() != b.size()) die("point count mismatch %s vs %s", na.c_str(), nb.c_str());
  for (size_t i = 0; i < a.size(); i++)
  {
    const DllPointE& x = a[i];
    const DllPointE& y = b[i];
    if (x.X != y.X || x.Y != y.Y || x.Z != y.Z || x.intensity != y.intensity ||
        x.ret != y.ret || x.nret != y.nret || x.sdf != y.sdf || x.edge != y.edge ||
        x.classification != y.classification || x.flags != y.flags ||
        x.user_data != y.user_data || x.channel != y.channel || x.psid != y.psid ||
        x.scan_angle != y.scan_angle || x.gps_time != y.gps_time ||
        x.rgb[0] != y.rgb[0] || x.rgb[1] != y.rgb[1] || x.rgb[2] != y.rgb[2] ||
        x.rgb[3] != y.rgb[3])
      die("point %zu differs between %s and %s (class %u vs %u, ret %u/%u vs %u/%u, "
          "angle %d vs %d, channel %u vs %u)",
          i, na.c_str(), nb.c_str(), x.classification, y.classification,
          x.ret, x.nret, y.ret, y.nret, x.scan_angle, y.scan_angle, x.channel, y.channel);
  }
}

// describe the raw on-disk VLR layout of a (compat) file for the oracle JSON
static std::string describe_disk_vlrs(const LasFile& f, std::string& compat_marker,
                                      std::string& eb_names)
{
  std::string vlrs;
  compat_marker = "null";
  eb_names = "";
  size_t pos = f.header_size;
  for (uint32_t i = 0; i < f.num_vlrs; i++)
  {
    const uint8_t* vlr = f.bytes.data() + pos;
    char user_id[17] = { 0 };
    memcpy(user_id, vlr + 2, 16);
    uint16_t record_id = rd_u16(vlr + 18);
    uint16_t len = rd_u16(vlr + 20);
    char buf[256];
    snprintf(buf, sizeof(buf), "%s{\"user_id\": \"%s\", \"record_id\": %u, \"length\": %u}",
             i ? ", " : "", user_id, record_id, len);
    vlrs += buf;
    if (strcmp(user_id, "lascompatible") == 0)
    {
      snprintf(buf, sizeof(buf), "{\"user_id\": \"%s\", \"record_id\": %u}", user_id, record_id);
      compat_marker = buf;
    }
    if (strcmp(user_id, "LASF_Spec") == 0 && record_id == 4)
    {
      for (uint16_t d = 0; d + 192 <= len; d += 192)
      {
        char name[33] = { 0 };
        memcpy(name, vlr + 54 + d + 4, 32);
        if (!eb_names.empty()) eb_names += ", ";
        eb_names += "\"" + json_escape(name) + "\"";
      }
    }
    pos += 54 + len;
  }
  return vlrs;
}

// generate one compatibility fixture set: .laz (compat), .las (native), .csv
// (from the DLL read-back of the .laz) and its compat_oracle.json entry
static std::string write_group_e_one(const std::vector<GenPointE>& pts, uint8_t pdf,
                                     const std::string& outdir, const std::string& stem)
{
  const bool pf8 = (pdf == 8);
  const uint16_t reclen = pf8 ? (uint16_t)38 : (uint16_t)30;
  const std::string laz = outdir + "/cpporacle/compat/" + stem + ".laz";
  const std::string las = outdir + "/cpporacle/compat/" + stem + ".las";
  const std::string csv = outdir + "/cpporacle/compat/" + stem + ".csv";

  // write compatibility-mode .laz and native uncompressed .las
  dll_write_file(pts, pdf, reclen, true, laz);
  dll_write_file(pts, pdf, reclen, false, las);

  // read both back with the DLL; the .laz read reconstructs the LAS 1.4
  // points from the compatibility extra bytes
  DllHeaderInfo laz_info{}, las_info{};
  std::vector<DllPointE> from_laz = dll_read_file(laz, (uint32_t)pts.size(), &laz_info);
  std::vector<DllPointE> from_las = dll_read_file(las, (uint32_t)pts.size(), &las_info);

  // self-verify: the reconstructed compat points must field-match the native ones
  compare_dll_points(from_laz, from_las, laz, las);
  printf("  verify %s: 1000/1000 reconstructed points field-match the native %s\n",
         laz.c_str(), las.c_str());

  // sanity: reconstructed values must also match the generated arrays
  for (size_t i = 0; i < pts.size(); i++)
  {
    const GenPointE& q = pts[i];
    const DllPointE& d = from_laz[i];
    if (d.X != q.X || d.classification != q.classification || d.ret != q.return_number ||
        d.nret != q.number_of_returns || d.scan_angle != q.scan_angle ||
        d.channel != q.scanner_channel || d.flags != q.flags || d.gps_time != q.gps_time ||
        (pf8 && (d.rgb[0] != q.red || d.rgb[3] != q.nir)))
      die("%s: reconstructed point %zu does not match generated values", laz.c_str(), i);
  }

  // CSV oracle from the DLL read-back of the .laz
  FILE* cf = fopen(csv.c_str(), "wb");
  if (!cf) die("cannot open %s", csv.c_str());
  fprintf(cf, "x,y,z,intensity,return_number,number_of_returns,scan_direction_flag,"
              "edge_of_flight_line,classification,user_data,point_source_id,scan_angle,"
              "gps_time,red,green,blue,nir,wavepacket_index,wavepacket_offset,"
              "wavepacket_size,return_point_wave_location,x_t,y_t,z_t,GridID,Confidence\n");
  for (const DllPointE& d : from_laz)
  {
    fprintf(cf, "%.3f,%.3f,%.3f,%u,%u,%u,%u,%u,%u,%u,%u,%d,%.17g,%u,%u,%u,%u,"
                "0,0,0,0,0,0,0,0,0\n",
            d.X * 0.001, d.Y * 0.001, d.Z * 0.001, d.intensity, d.ret, d.nret,
            d.sdf, d.edge, d.classification, d.user_data, d.psid, (int)d.scan_angle,
            d.gps_time, d.rgb[0], d.rgb[1], d.rgb[2], pf8 ? d.rgb[3] : 0);
  }
  fclose(cf);
  printf("wrote %s (populated by the DLL reader's compat reconstruction)\n", csv.c_str());

  // on-disk facts for the oracle JSON
  LasFile disk = parse_las(laz);
  std::string compat_marker, eb_names;
  std::string vlrs = describe_disk_vlrs(disk, compat_marker, eb_names);

  char buf[2048];
  snprintf(buf, sizeof(buf),
           "  \"%s.laz\": {\n"
           "    \"on_disk\": {\"point_data_format_byte\": %u, \"point_data_format\": %u, "
           "\"compressed_bit\": %s, \"point_data_record_length\": %u, \"version_minor\": %u, "
           "\"header_size\": %u, \"legacy_number_of_point_records\": %u},\n"
           "    \"on_disk_vlrs\": [%s],\n"
           "    \"compatibility_marker_vlr\": %s,\n"
           "    \"extra_bytes_descriptor_names\": [%s],\n"
           "    \"dll_reader_reports\": {\"point_data_format\": %u, "
           "\"point_data_record_length\": %u, \"version_minor\": %u, "
           "\"number_of_variable_length_records\": %u, \"attributes_visible\": %d, "
           "\"extra_bytes_visible\": %d, \"number_of_point_records\": %llu}\n"
           "  }",
           stem.c_str(),
           disk.bytes[104], disk.point_data_format, disk.compressed ? "true" : "false",
           disk.record_length, disk.bytes[25], disk.header_size, disk.num_points,
           vlrs.c_str(), compat_marker.c_str(), eb_names.c_str(),
           laz_info.point_data_format, laz_info.point_data_record_length,
           laz_info.version_minor, laz_info.num_vlrs, laz_info.attributes_visible,
           (int)laz_info.point_data_record_length - (pf8 ? 38 : 30),
           (unsigned long long)laz_info.npoints);
  return buf;
}

static void write_group_e(const std::string& outdir)
{
  std::vector<GenPointE> pts = generate_points_e(1000);

  int switches = 0, gt31 = 0, ret_gt7 = 0;
  for (size_t i = 0; i < pts.size(); i++)
  {
    if (i && pts[i].scanner_channel != pts[i - 1].scanner_channel) switches++;
    if (pts[i].classification > 31) gt31++;
    if (pts[i].number_of_returns > 7) ret_gt7++;
  }
  printf("generated 1000 compat points: %d channel switches, %d classifications > 31, "
         "%d points with number_of_returns > 7\n", switches, gt31, ret_gt7);

  std::string j6 = write_group_e_one(pts, 6, outdir, "las14_pf6_compat_1000pts");
  std::string j8 = write_group_e_one(pts, 8, outdir, "las14_pf8_compat_1000pts");

  std::string json = "{\n" + j6 + ",\n" + j8 + "\n}\n";
  const std::string jpath = outdir + "/cpporacle/compat/compat_oracle.json";
  FILE* jf = fopen(jpath.c_str(), "wb");
  if (!jf) die("cannot open %s", jpath.c_str());
  fwrite(json.data(), 1, json.size(), jf);
  fclose(jf);
  printf("wrote %s\n", jpath.c_str());
  printf("%s", json.c_str());
}

// ----------------------------------------------------------------------------
// main
// ----------------------------------------------------------------------------

int main(int argc, char* argv[])
{
  if (argc != 4)
  {
    fprintf(stderr, "usage: %s <input.las> <input.laz> <outdir>\n", argv[0]);
    return 1;
  }
  const std::string in_las = argv[1];
  const std::string in_laz = argv[2];
  const std::string outdir = argv[3];

  make_dir(outdir);
  make_dir(outdir + "/las");
  make_dir(outdir + "/cpporacle");
  make_dir(outdir + "/cpporacle/corrupt");
  make_dir(outdir + "/cpporacle/compat");

  // ------------------------------- inputs -------------------------------
  LasFile las = parse_las(in_las);
  if (las.point_data_format != 3 || las.record_length != 42 || las.num_points != 1000)
    die("unexpected input .las: pdf=%u reclen=%u count=%u",
        las.point_data_format, las.record_length, las.num_points);
  LasFile base_laz = parse_las(in_laz);
  if (!base_laz.compressed) die("input .laz is not compressed");
  printf("input %s: pdf=%u reclen=%u points=%u offset=%u\n", in_las.c_str(),
         las.point_data_format, las.record_length, las.num_points,
         las.offset_to_point_data);

  // ------------------------------- GROUP A -------------------------------
  printf("\n=== Group A: v1 re-compressions ===\n");
  const std::string a1 = outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1.laz";
  const std::string a3 = outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1pw.laz";
  write_laz_group_a(las, LASZIP_COMPRESSOR_POINTWISE_CHUNKED, 1, 100, a1);
  write_laz_group_a(las, LASZIP_COMPRESSOR_POINTWISE, 1, -1, a3);
  check_pointwise_no_chunk_table(a3, las);
  // byte-identical .las twins so the Go e2e sweep pairs them
  write_file(outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1.las", las.bytes);
  write_file(outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1pw.las", las.bytes);
  printf("wrote %s\n", (outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1.las").c_str());
  printf("wrote %s\n", (outdir + "/las/las12_pf3_1000pts_with_extrabytes_v1pw.las").c_str());

  // ------------------------------- GROUP B -------------------------------
  printf("\n=== Group B: LAS 1.4 multichannel fixtures ===\n");
  int switches = 0, revisits = 0;
  std::vector<GenPoint> pts = generate_points(1000, switches, revisits);
  printf("generated 1000 points: %d scanner-channel switches, %d switches to a "
         "channel already used within the same 100-point chunk\n", switches, revisits);
  int chan_hist[4] = { 0, 0, 0, 0 };
  for (const GenPoint& p : pts) chan_hist[p.scanner_channel]++;
  printf("channel histogram: ch0=%d ch1=%d ch2=%d ch3=%d\n",
         chan_hist[0], chan_hist[1], chan_hist[2], chan_hist[3]);

  GroupBFiles f7 = {
    outdir + "/cpporacle/las14_pf7_v3_multichannel_1000pts.las",
    outdir + "/cpporacle/las14_pf7_v3_multichannel_1000pts.laz",
    outdir + "/cpporacle/las14_pf7_v3_multichannel_1000pts.csv",
  };
  write_group_b(pts, 7, 3, f7);

  GroupBFiles f8 = {
    outdir + "/cpporacle/las14_pf8_v4_multichannel_1000pts.las",
    outdir + "/cpporacle/las14_pf8_v4_multichannel_1000pts.laz",
    outdir + "/cpporacle/las14_pf8_v4_multichannel_1000pts.csv",
  };
  write_group_b(pts, 8, 4, f8);

  // ------------------------------- GROUP C -------------------------------
  printf("\n=== Group C: corrupt variants of %s ===\n", in_laz.c_str());
  const uint32_t off = base_laz.offset_to_point_data;
  const size_t fsize = base_laz.bytes.size();
  printf("base .laz: offset_to_point_data=%u file_size=%zu\n", off, fsize);

  // 7: chunk table offset slot = -1 (0xFF x 8)
  std::vector<uint8_t> c1 = base_laz.bytes;
  memset(&c1[off], 0xFF, 8);
  const std::string p1 = outdir + "/cpporacle/corrupt/chunked_tableoffset_minus1.laz";
  write_file(p1, c1);

  // 8: chunk table offset slot = offset_to_point_data + 500 (mid compressed data)
  std::vector<uint8_t> c2 = base_laz.bytes;
  wr_u64(&c2[off], (uint64_t)off + 500);
  const std::string p2 = outdir + "/cpporacle/corrupt/chunked_tableoffset_garbage.laz";
  write_file(p2, c2);

  // 9: truncate to offset + 60% of the point-data byte size (cut mid-chunk)
  size_t point_bytes = fsize - off;
  size_t keep = off + (point_bytes * 6) / 10;
  std::vector<uint8_t> c3(base_laz.bytes.begin(), base_laz.bytes.begin() + keep);
  const std::string p3 = outdir + "/cpporacle/corrupt/chunked_truncated.laz";
  write_file(p3, c3);
  printf("wrote corrupt fixtures (truncated file keeps %zu of %zu bytes)\n", keep, fsize);

  // run the C++ oracle over each corrupt file
  std::string json = "{\n";
  const char* names[3] = { "chunked_tableoffset_minus1.laz",
                           "chunked_tableoffset_garbage.laz",
                           "chunked_truncated.laz" };
  const std::string paths[3] = { p1, p2, p3 };
  for (int i = 0; i < 3; i++)
  {
    OracleResult r = run_oracle(paths[i], las);
    printf("oracle %s: open_ok=%d points_ok=%u error_at=%d matches_las=%d\n"
           "         error=%s\n         warning=%s\n",
           names[i], r.open_ok, r.points_ok, r.error_at, r.matches_las,
           r.error_msg.empty() ? "(none)" : r.error_msg.c_str(),
           r.warning_msg.empty() ? "(none)" : r.warning_msg.c_str());
    append_oracle_json(json, names[i], r, i == 2);
  }
  json += "}\n";
  const std::string jpath = outdir + "/cpporacle/corrupt/oracle.json";
  FILE* jf = fopen(jpath.c_str(), "wb");
  if (!jf) die("cannot open %s", jpath.c_str());
  fwrite(json.data(), 1, json.size(), jf);
  fclose(jf);
  printf("wrote %s\n", jpath.c_str());

  // ------------------------------- GROUP D -------------------------------
  printf("\n=== Group D: mid-chunk corruption salvage oracle ===\n");
  write_group_d(base_laz, las, in_laz, outdir);

  // ------------------------------- GROUP E -------------------------------
  printf("\n=== Group E: LAS 1.4 compatibility-mode fixtures (DLL API) ===\n");
  write_group_e(outdir);

  printf("\nALL FIXTURES GENERATED AND SELF-VERIFIED OK\n");
  return 0;
}
