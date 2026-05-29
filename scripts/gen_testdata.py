# gen_testdata.py — generate LAS test fixtures for the golaz test suite.
#
# Workflow:
#   1. Run this script (requires laspy, numpy, pandas):
#        python scripts/gen_testdata.py
#      Output lands in comprehensive_test_suite/
#
#   2. Laszip the generated .las files with chunk_size=100:
#        for f in comprehensive_test_suite/*.las; do
#            laszip -i "$f" -o "${f%.las}.laz" -chunk_size 100
#        done
#
#   3. Copy/replace the fixtures into the test data directory:
#        cp comprehensive_test_suite/*.{las,laz,csv} internal/laz/testdata/las/
#
# The script always generates 10-point files (seed=42, uniform random) and
# 1000-point files (seed=42, random walk — first 1000 pts of a 60000-pt walk
# Extra bytes (GridID uint32 + Confidence float32)
# are injected into all 1000-point files.

import laspy
import numpy as np
import pandas as pd
import os

SPEC_MATRIX = {
    "1.2": [0, 1, 2, 3],
    "1.3": [0, 1, 2, 3, 4, 5],
    "1.4": [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
}
POINT_COUNTS = [10, 1000]

SCALE = [0.001, 0.001, 0.001]
OFFSET = [0.0, 0.0, 0.0]

# Internal generation size for the random-walk dataset. Keeping this fixed
# Changing this number will affect the random numbers being generated so it
# is adviced to leave it as is to generate reproducible data.
_WALK_GEN_SIZE = 60000


def generate_master_arrays(count):
    rng = np.random.default_rng(seed=42)

    if count > 10:
        start_x = rng.uniform(1000.0, 5000.0)
        start_y = rng.uniform(1000.0, 5000.0)
        start_z = rng.uniform(0.0, 100.0)

        steps_x = rng.uniform(0.05, 0.20, _WALK_GEN_SIZE)
        steps_x[0] = 0.0
        steps_y = rng.uniform(0.05, 0.20, _WALK_GEN_SIZE)
        steps_y[0] = 0.0
        steps_z = rng.uniform(0.05, 0.20, _WALK_GEN_SIZE)
        steps_z[0] = 0.0

        raw_x = start_x + np.cumsum(steps_x)
        raw_y = start_y + np.cumsum(steps_y)
        raw_z = start_z + np.cumsum(steps_z)

        x_coords = (np.round((raw_x - OFFSET[0]) / SCALE[0]) * SCALE[0] + OFFSET[0])[:count]
        y_coords = (np.round((raw_y - OFFSET[1]) / SCALE[1]) * SCALE[1] + OFFSET[1])[:count]
        z_coords = (np.round((raw_z - OFFSET[2]) / SCALE[2]) * SCALE[2] + OFFSET[2])[:count]

        master = {
            'x': x_coords,
            'y': y_coords,
            'z': z_coords,
            'intensity':            rng.integers(0, 65535, _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'return_number':        rng.integers(1, 4,     _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'number_of_returns':    rng.integers(1, 4,     _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'scan_direction_flag':  rng.integers(0, 2,     _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'edge_of_flight_line':  rng.integers(0, 2,     _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'classification':       rng.integers(0, 32,    _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'user_data':            rng.integers(0, 255,   _WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'point_source_id':      rng.integers(1, 100,   _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'scan_angle':           rng.integers(-90, 90,  _WALK_GEN_SIZE, dtype=np.int16)[:count],
            'gps_time':             np.linspace(400000.0, 400100.0, _WALK_GEN_SIZE, dtype=np.float64)[:count],
            'red':                  rng.integers(0, 65535, _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'green':                rng.integers(0, 65535, _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'blue':                 rng.integers(0, 65535, _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'nir':                  rng.integers(0, 65535, _WALK_GEN_SIZE, dtype=np.uint16)[:count],
            'wavepacket_index':     np.zeros(_WALK_GEN_SIZE, dtype=np.uint8)[:count],
            'wavepacket_offset':    (np.arange(_WALK_GEN_SIZE, dtype=np.uint64) * 64)[:count],
            'wavepacket_size':      (np.ones(_WALK_GEN_SIZE, dtype=np.uint32) * 32)[:count],
            'return_point_wave_location': rng.uniform(0.0, 10.0, _WALK_GEN_SIZE).astype(np.float32)[:count],
            'x_t':                  rng.uniform(-1.0, 1.0, _WALK_GEN_SIZE).astype(np.float32)[:count],
            'y_t':                  rng.uniform(-1.0, 1.0, _WALK_GEN_SIZE).astype(np.float32)[:count],
            'z_t':                  rng.uniform(-1.0, 1.0, _WALK_GEN_SIZE).astype(np.float32)[:count],
            'GridID':               rng.integers(100000, 999999, _WALK_GEN_SIZE, dtype=np.uint32)[:count],
            'Confidence':           rng.uniform(0.0, 1.0, _WALK_GEN_SIZE).astype(np.float32)[:count],
        }
    else:
        raw_x = rng.uniform(1000.0, 1100.0, count)
        raw_y = rng.uniform(1000.0, 1100.0, count)
        raw_z = rng.uniform(0.0, 10.0, count)

        x_coords = np.round((raw_x - OFFSET[0]) / SCALE[0]) * SCALE[0] + OFFSET[0]
        y_coords = np.round((raw_y - OFFSET[1]) / SCALE[1]) * SCALE[1] + OFFSET[1]
        z_coords = np.round((raw_z - OFFSET[2]) / SCALE[2]) * SCALE[2] + OFFSET[2]

        master = {
            'x': x_coords,
            'y': y_coords,
            'z': z_coords,
            'intensity':            rng.integers(0, 65535, count, dtype=np.uint16),
            'return_number':        rng.integers(1, 4,     count, dtype=np.uint8),
            'number_of_returns':    rng.integers(1, 4,     count, dtype=np.uint8),
            'scan_direction_flag':  rng.integers(0, 2,     count, dtype=np.uint8),
            'edge_of_flight_line':  rng.integers(0, 2,     count, dtype=np.uint8),
            'classification':       rng.integers(0, 32,    count, dtype=np.uint8),
            'user_data':            rng.integers(0, 255,   count, dtype=np.uint8),
            'point_source_id':      rng.integers(1, 100,   count, dtype=np.uint16),
            'scan_angle':           rng.integers(-90, 90,  count, dtype=np.int16),
            'gps_time':             np.linspace(400000.0, 400100.0, count, dtype=np.float64),
            'red':                  rng.integers(0, 65535, count, dtype=np.uint16),
            'green':                rng.integers(0, 65535, count, dtype=np.uint16),
            'blue':                 rng.integers(0, 65535, count, dtype=np.uint16),
            'nir':                  rng.integers(0, 65535, count, dtype=np.uint16),
            'wavepacket_index':     np.zeros(count, dtype=np.uint8),
            'wavepacket_offset':    np.arange(count, dtype=np.uint64) * 64,
            'wavepacket_size':      np.ones(count, dtype=np.uint32) * 32,
            'return_point_wave_location': rng.uniform(0.0, 10.0, count).astype(np.float32),
            'x_t':                  rng.uniform(-1.0, 1.0, count).astype(np.float32),
            'y_t':                  rng.uniform(-1.0, 1.0, count).astype(np.float32),
            'z_t':                  rng.uniform(-1.0, 1.0, count).astype(np.float32),
            'GridID':               rng.integers(100000, 999999, count, dtype=np.uint32),
            'Confidence':           rng.uniform(0.0, 1.0, count).astype(np.float32),
        }

    return master


def generate_multiscanner_arrays():
    master = generate_master_arrays(10)
    master['scanner_channel'] = (np.arange(10, dtype=np.uint8) % 2)
    return master


def populate_las_from_master(las, master):
    las.x = master['x']
    las.y = master['y']
    las.z = master['z']
    las.intensity = master['intensity']
    las.return_number = master['return_number']
    las.number_of_returns = master['number_of_returns']
    las.scan_direction_flag = master['scan_direction_flag']
    las.edge_of_flight_line = master['edge_of_flight_line']
    las.classification = master['classification']
    las.user_data = master['user_data']
    las.point_source_id = master['point_source_id']

    if hasattr(las, 'scan_angle'):
        las.scan_angle = master['scan_angle']
    elif hasattr(las, 'scan_angle_rank'):
        las.scan_angle_rank = master['scan_angle'].astype(np.int8)

    if hasattr(las, 'gps_time'):
        las.gps_time = master['gps_time']

    if hasattr(las, 'red'):
        las.red = master['red']
        las.green = master['green']
        las.blue = master['blue']

    if hasattr(las, 'nir'):
        las.nir = master['nir']

    if hasattr(las, 'wavepacket_index'):
        las.wavepacket_index = master['wavepacket_index']
        las.wavepacket_offset = master['wavepacket_offset']
        las.wavepacket_size = master['wavepacket_size']
        las.return_point_wave_location = master['return_point_wave_location']
        las.x_t = master['x_t']
        las.y_t = master['y_t']
        las.z_t = master['z_t']


def populate_las_multiscanner(las, master):
    populate_las_from_master(las, master)
    if hasattr(las, 'scanner_channel'):
        las.scanner_channel = master['scanner_channel']


def make_test_vlr():
    return laspy.VLR(
        user_id="TEST_METADATA",
        record_id=999,
        description="Unit Test Block",
        record_data=b"Test Matrix Verification Payload."
    )


def main():
    output_dir = "comprehensive_test_suite"
    os.makedirs(output_dir, exist_ok=True)

    print("Initializing master data pipeline...")

    master_data_cache = {count: generate_master_arrays(count) for count in POINT_COUNTS}

    for count, master in master_data_cache.items():
        csv_filename = f"{output_dir}/reference_{count}pts.csv"
        df = pd.DataFrame(master)
        df.to_csv(csv_filename, index=False)
        print(f"Reference CSV: {csv_filename}")

    print("\nGenerating standard LAS/LAZ file matrix...")

    for version, formats in SPEC_MATRIX.items():
        for fmt_id in formats:
            for count in POINT_COUNTS:
                inject_extrabytes = (count == 1000)
                suffix = "_with_extrabytes" if inject_extrabytes else ""
                filename = f"{output_dir}/las{version.replace('.', '')}_pf{fmt_id}_{count}pts{suffix}.las"

                try:
                    header = laspy.LasHeader(point_format=fmt_id, version=version)
                    header.offsets = OFFSET
                    header.scales = SCALE

                    las = laspy.LasData(header)
                    master = master_data_cache[count]

                    if inject_extrabytes:
                        las.add_extra_dim(laspy.ExtraBytesParams(name="GridID", type=np.uint32))
                        las.add_extra_dim(laspy.ExtraBytesParams(name="Confidence", type=np.float32))

                    populate_las_from_master(las, master)

                    if inject_extrabytes:
                        las.GridID = master['GridID']
                        las.Confidence = master['Confidence']

                    las.vlrs.append(make_test_vlr())
                    las.update_header()
                    las.write(filename)
                    print(f"OK: {filename}")

                except Exception as e:
                    print(f"FAIL [{version} pf{fmt_id} {count}pts]: {e}")

    print("\nGenerating multi-scanner LAS 1.4 files (pf6-10, 10 pts)...")

    ms_master = generate_multiscanner_arrays()

    for fmt_id in [6, 7, 8, 9, 10]:
        inject_extrabytes = (fmt_id == 6)
        suffix = "_with_extrabytes" if inject_extrabytes else ""
        filename = f"{output_dir}/las14_pf{fmt_id}_10pts_multiscanner{suffix}.las"

        try:
            header = laspy.LasHeader(point_format=fmt_id, version="1.4")
            header.offsets = OFFSET
            header.scales = SCALE

            las = laspy.LasData(header)

            if inject_extrabytes:
                las.add_extra_dim(laspy.ExtraBytesParams(name="GridID", type=np.uint32))
                las.add_extra_dim(laspy.ExtraBytesParams(name="Confidence", type=np.float32))

            populate_las_multiscanner(las, ms_master)

            if inject_extrabytes:
                las.GridID = ms_master['GridID']
                las.Confidence = ms_master['Confidence']

            las.vlrs.append(make_test_vlr())
            las.update_header()
            las.write(filename)
            print(f"OK: {filename}")

        except Exception as e:
            print(f"FAIL [LAS 1.4 pf{fmt_id} multiscanner]: {e}")

    print("\nDone.")


if __name__ == "__main__":
    main()
