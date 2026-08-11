FROM golang:1.25@sha256:2ddaec94e9a119c926e843982c01e15c1d4d22a05bd5db8248722d1c50b7cca9 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY api/ api/
COPY cmd/ cmd/
COPY controllers/ controllers/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o /manager ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:23795be0fe67b7d47d1ee62b19c7db750152db627d5bbfa31307e892a7575bec
WORKDIR /
COPY --from=builder /manager /manager

# The image is a redistribution of this work and of every dependency linked
# into the binary, so it carries their terms. /licenses is where image tooling
# and Red Hat certification look. Copied from the build context rather than the
# builder stage, so a change here cannot be masked by a stale build layer.
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /licenses/

## https://github.com/opencontainers/image-spec/blob/main/annotations.md
LABEL org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.title="cluster-api-provider-nico"

USER 65532:65532

ENTRYPOINT ["/manager"]
