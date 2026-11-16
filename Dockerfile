# SPDX-License-Identifier: MIT

# lake-sqlite: one static binary over one SQLite database, built with modernc.org/sqlite (pure Go,
# BSD-3-Clause), so CGO stays off and the runtime is `scratch`: the binary, a two-line /etc/passwd
# and /etc/group naming the nonroot user 65532, and an empty world-writable /tmp for SQLite's
# temporary files. No shell, no libc, no distribution packages, so nothing GPL or LGPL ships in the
# image. There is no CA bundle: the service makes no outbound TLS connections. The builder image is
# digest-pinned.
#
#   docker buildx build --platform linux/amd64 -t oramasearch/lake-sqlite:<tag> .
FROM golang:1.23@sha256:60deed95d3888cc5e4d9ff8a10c54e5edc008c6ae3fba6187be6fb592e19e8c0 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /workspace
# Module downloads first, so a source-only change reuses the layer.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/lake-sqlite ./cmd/lake-sqlite
# The runtime filesystem, written here because scratch has no shell to write it.
RUN mkdir -p /rootfs/etc /rootfs/tmp \
    && chmod 1777 /rootfs/tmp \
    && echo 'nonroot:x:65532:65532:nonroot:/nonexistent:/sbin/nologin' > /rootfs/etc/passwd \
    && echo 'nonroot:x:65532:' > /rootfs/etc/group

FROM scratch
COPY --from=builder /rootfs/ /
COPY --from=builder /out/lake-sqlite /lake-sqlite
USER 65532:65532
EXPOSE 8080
# The database lives on the mounted volume; the Orama operator's StatefulSet mounts it at /data.
ENV LAKE_SQLITE_PATH=/data/main.db
ENTRYPOINT ["/lake-sqlite"]
