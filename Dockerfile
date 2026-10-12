# syntax=docker/dockerfile:1
# Print pipeline: one image with the MCP server, the viewer, OpenSCAD and Bambu Studio.
# Bambu Studio ships Linux builds for x86_64 only, so the runtime is linux/amd64; the
# build stages run natively and cross-compile.

# 1. Frontend: unit tests, then static files
FROM --platform=$BUILDPLATFORM node:20-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json web/.npmrc ./
RUN npm ci
COPY web/ ./
RUN npx vitest run && npm run build

# 2. Backend: unit tests, then a static linux/amd64 binary with the frontend embedded
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./internal/web/static
ARG VERSION=dev
RUN go vet ./... && go test ./... \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/printpipe ./cmd/printpipe

# 3. Slicer: unpack the pinned Bambu Studio AppImage without running it (no FUSE in Docker,
# and the AppImage runtime can't execute under emulation). The squashfs payload starts
# where the ELF section header table ends.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS slicer
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates squashfs-tools python3 \
    && rm -rf /var/lib/apt/lists/*
ARG BAMBU_APPIMAGE_URL=https://github.com/bambulab/BambuStudio/releases/download/v02.08.02.61/BambuStudio_ubuntu24.04-v02.08.02.61-20260820225108.AppImage
RUN test -n "$BAMBU_APPIMAGE_URL" \
 && curl -fL --retry 3 "$BAMBU_APPIMAGE_URL" -o /tmp/bs.AppImage \
 && offset=$(python3 -c 'import struct,sys; b=open(sys.argv[1],"rb").read(64); shoff,=struct.unpack_from("<Q",b,0x28); shentsize,shnum=struct.unpack_from("<HH",b,0x3A); print(shoff+shentsize*shnum)' /tmp/bs.AppImage) \
 && unsquashfs -q -n -o "$offset" -d /opt/bambu-studio /tmp/bs.AppImage \
 && rm /tmp/bs.AppImage && test -x /opt/bambu-studio/AppRun

# 4. Runtime
FROM --platform=linux/amd64 ubuntu:24.04 AS runtime
ENV DEBIAN_FRONTEND=noninteractive LANG=C.UTF-8 LC_ALL=C.UTF-8
RUN apt-get update && apt-get install -y --no-install-recommends \
      openscad xvfb xauth curl ca-certificates tini \
      libgtk-3-0t64 libwebkit2gtk-4.1-0 libgl1 libegl1 libglu1-mesa libgl1-mesa-dri \
      libgstreamer1.0-0 libgstreamer-plugins-base1.0-0 libsoup-3.0-0 libsecret-1-0 \
      libosmesa6 libfuse2t64 locales fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*

COPY --from=slicer /opt/bambu-studio /opt/bambu-studio
ENV BAMBU_STUDIO=/opt/bambu-studio/AppRun \
    OPENSCAD=openscad \
    PRINTPIPE_DATA=/data \
    PRINTPIPE_WORK=/work

COPY --from=go /out/printpipe /usr/local/bin/printpipe
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && mkdir -p /data /work
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "/entrypoint.sh"]

# 5. Integration tests against the real OpenSCAD and Bambu Studio: docker build --target integration .
FROM --platform=linux/amd64 golang:1.26-bookworm AS gotools
FROM runtime AS integration
COPY --from=gotools /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH GOFLAGS=-mod=mod CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN xvfb-run -a -s "-screen 0 1280x1024x24" go test -count=1 -tags integration -v ./internal/scad/... ./internal/slicers/... ./internal/printers/bambu/...

# The default image is the runtime.
FROM runtime
