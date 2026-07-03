# Test-fixture generator for golaz.
#
# Builds the genfixtures harness against the REAL LASzip C++ sources, pinned
# to upstream commit ac3e9e9f954427872c8c77b68b7766cf706322f9 (the exact
# commit the golaz port was written against).
#
# Run with:
#   docker run --rm -v <testdata/las>:/in:ro -v <outdir>:/out golaz-fixturegen

FROM debian:bookworm-slim AS build

RUN apt-get update \
 && apt-get install -y --no-install-recommends g++ git ca-certificates \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /build

ARG LASZIP_COMMIT=ac3e9e9f954427872c8c77b68b7766cf706322f9
RUN git clone https://github.com/LASzip/LASzip.git \
 && cd LASzip \
 && git checkout ${LASZIP_COMMIT}

COPY genfixtures.cpp .

# Compile the harness directly against ALL of the LASzip sources, including
# laszip_dll.cpp compiled statically into the harness (needed for the Group E
# compatibility-mode fixtures, whose recoding logic lives in the DLL layer).
# - -DLASZIPDLL_EXPORTS makes lasindex.cpp include lasreadpoint.hpp instead
#   of the LASlib-only lasreader.hpp (this is how the upstream DLL builds).
# - laszipper.cpp / lasunzipper.cpp (the deprecated libLAS binding, which is
#   exactly the simple point-in/point-out interface we want) reference an
#   IS_LITTLE_ENDIAN() macro that the default upstream build no longer
#   defines, so it is provided on the command line in terms of the runtime
#   check from mydefs.hpp.
RUN g++ -O2 -std=c++17 -I LASzip/src -I LASzip/include/laszip -I LASzip/dll \
      -DLASZIPDLL_EXPORTS \
      -D'IS_LITTLE_ENDIAN()=(Endian::IS_LITTLE_ENDIAN)' \
      -o /usr/local/bin/genfixtures \
      genfixtures.cpp \
      LASzip/src/*.cpp

FROM debian:bookworm-slim
COPY --from=build /usr/local/bin/genfixtures /usr/local/bin/genfixtures
ENTRYPOINT ["genfixtures", \
            "/in/las12_pf3_1000pts_with_extrabytes.las", \
            "/in/las12_pf3_1000pts_with_extrabytes.laz", \
            "/out"]
