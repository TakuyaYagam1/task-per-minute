# syntax=docker/dockerfile:1.7
FROM golang:1.26.8-alpine3.23 AS go-base

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

FROM go-base AS builder

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags='-s -w' \
        -o /out/server ./cmd/app
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags='-s -w' \
        -o /out/migrate ./cmd/migrate

FROM go-base AS account-seed-builder

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath -ldflags='-s -w' \
        -o /out/testseed ./cmd/testseed

FROM go-base AS test-bots-builder
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/testbots ./cmd/testbots && \
    CGO_ENABLED=0 GOOS=linux go build -tags=testtools -trimpath -ldflags='-s -w' -o /out/test-server ./cmd/app

FROM alpine:3.24 AS media-builder

RUN apk add --no-cache \
        build-base \
        ca-certificates \
        gnupg \
        nasm \
        pkgconf \
        wget \
        x264-dev \
        xz

RUN set -eux; \
    version=9.0.2; \
    expected_sha256=8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e; \
    expected_fingerprint=FCF986EA15E6E293A5644F10B4322F04D67658D8; \
    archive="ffmpeg-${version}.tar.xz"; \
    mkdir -m 0700 -p /tmp/gnupg /tmp/ffmpeg-src /out; \
    wget -q -O "/tmp/${archive}" "https://ffmpeg.org/releases/${archive}"; \
    wget -q -O "/tmp/${archive}.asc" "https://ffmpeg.org/releases/${archive}.asc"; \
    wget -q -O /tmp/ffmpeg-devel.asc https://ffmpeg.org/ffmpeg-devel.asc; \
    printf '%s  %s\n' "$expected_sha256" "/tmp/${archive}" | sha256sum -c -; \
    gpg --batch --homedir /tmp/gnupg --import /tmp/ffmpeg-devel.asc; \
    gpg --batch --homedir /tmp/gnupg --status-file /tmp/ffmpeg-gpg.status \
        --verify "/tmp/${archive}.asc" "/tmp/${archive}"; \
    awk -v fingerprint="$expected_fingerprint" \
        '$1 == "[GNUPG:]" && $2 == "VALIDSIG" && $3 == fingerprint { valid = 1 } END { exit !valid }' \
        /tmp/ffmpeg-gpg.status; \
    tar -xf "/tmp/${archive}" --strip-components=1 -C /tmp/ffmpeg-src; \
    cd /tmp/ffmpeg-src; \
    ./configure \
        --prefix=/usr/local \
        --bindir=/out \
        --enable-gpl \
        --enable-libx264 \
        --enable-static \
        --disable-shared \
        --disable-everything \
        --disable-programs \
        --disable-autodetect \
        --disable-network \
        --disable-devices \
        --disable-doc \
        --disable-ffplay \
        --enable-ffmpeg \
        --enable-ffprobe \
        --enable-decoder=h264,hevc,mpeg4 \
        --enable-parser=h264,hevc,mpeg4video \
        --enable-encoder=libx264 \
        --enable-demuxer=mov \
        --enable-muxer=mp4 \
        --enable-protocol=file \
        --enable-filter=fps,scale; \
    make -j2; \
    make install; \
    cp COPYING.GPLv2 /out/COPYING.GPLv2; \
    printf '%s\n' \
        'FFmpeg 9.0.2 built from https://ffmpeg.org/releases/ffmpeg-9.0.2.tar.xz' \
        'SHA-256: 8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e' \
        'Upstream signing key fingerprint: FCF986EA15E6E293A5644F10B4322F04D67658D8' \
        'Configure: --enable-gpl --enable-libx264 --enable-static --disable-shared --disable-everything --disable-programs --disable-autodetect --disable-network --disable-devices --disable-doc --disable-ffplay --enable-ffmpeg --enable-ffprobe --enable-decoder=h264,hevc,mpeg4 --enable-parser=h264,hevc,mpeg4video --enable-encoder=libx264 --enable-demuxer=mov --enable-muxer=mp4 --enable-protocol=file --enable-filter=fps,scale' \
        > /out/BUILD.txt; \
    rm -rf /tmp/gnupg /tmp/ffmpeg-src "/tmp/${archive}" "/tmp/${archive}.asc" \
        /tmp/ffmpeg-devel.asc /tmp/ffmpeg-gpg.status

FROM go-base AS media-check

COPY --from=media-builder /out/ffmpeg /usr/local/bin/ffmpeg
COPY --from=media-builder /out/ffprobe /usr/local/bin/ffprobe
COPY --from=media-builder /usr/lib/libx264.so.* /usr/lib/

WORKDIR /src
RUN GOTOOLCHAIN=local go mod download
ENV GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
CMD ["go", "test", "-tags=media_integration", "./internal/adapter/outbound/media/ffmpeg"]

FROM alpine:3.24 AS account-seed

RUN addgroup -g 1001 -S appgroup && \
    adduser -S -u 1001 -G appgroup appuser && \
    mkdir -p /app /seed && \
    chown 1001:1001 /app /seed && \
    chmod 0700 /seed

COPY --from=go-base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=account-seed-builder --chown=1001:1001 /out/testseed /app/testseed

WORKDIR /app
USER 1001:1001
ENTRYPOINT ["/app/testseed"]

FROM alpine:3.24 AS runtime-base

ARG BACKEND_PORT=8080

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -g 1001 -S appgroup && \
    adduser -S -u 1001 -G appgroup appuser

WORKDIR /app

COPY --from=builder --chown=appuser:appgroup /out/server /app/server
COPY --from=builder --chown=appuser:appgroup /out/migrate /app/migrate
COPY --from=builder --chown=appuser:appgroup /src/db/migrations /app/migrations
COPY --from=media-builder --chown=appuser:appgroup /out/ffmpeg /usr/local/bin/ffmpeg
COPY --from=media-builder --chown=appuser:appgroup /out/ffprobe /usr/local/bin/ffprobe
COPY --from=media-builder /usr/lib/libx264.so.* /usr/lib/
COPY --from=media-builder /out/COPYING.GPLv2 /usr/local/share/licenses/ffmpeg/COPYING.GPLv2
COPY --from=media-builder /out/BUILD.txt /usr/local/share/doc/ffmpeg/BUILD.txt

USER appuser
ENV HTTP_PORT=${BACKEND_PORT}
EXPOSE ${BACKEND_PORT}

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget --quiet --tries=1 -O /dev/null "http://127.0.0.1:${HTTP_PORT:-8080}/health" || exit 1

ENTRYPOINT ["/app/server"]

FROM runtime-base AS test-backend
USER root
RUN mkdir -p /bot-control && chown 1001:1001 /bot-control && chmod 0700 /bot-control
USER appuser
COPY --from=test-bots-builder --chown=1001:1001 /out/test-server /app/server

FROM alpine:3.24 AS test-bots
RUN addgroup -g 1001 -S appgroup && adduser -S -u 1001 -G appgroup appuser && \
    mkdir -p /app /state && chown 1001:1001 /app /state && chmod 0700 /state
COPY --from=go-base /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=test-bots-builder --chown=1001:1001 /out/testbots /app/testbots
USER 1001:1001
ENTRYPOINT ["/app/testbots"]

# Building without an explicit target always produces the production server.
FROM runtime-base AS runtime
