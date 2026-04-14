# syntax=docker/dockerfile:1.7

# --- Build Stage ---
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

# Copy the entire repo to leverage local replaces.
COPY . .

WORKDIR /src
RUN go mod download

# Build the binary.
RUN set -eux; \
    : "${TARGETARCH:?TARGETARCH not set}"; \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" go build -o livekit-turn-detector-engram .

# --- Final Stage ---
FROM --platform=$TARGETPLATFORM gcr.io/distroless/static:nonroot

COPY --from=builder /src/livekit-turn-detector-engram /livekit-turn-detector-engram

ENTRYPOINT ["/livekit-turn-detector-engram"]
