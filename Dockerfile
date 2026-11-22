# syntax=docker/dockerfile:1.7
FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Reported by /readyz. .git is not in the build context, so pass the release version and
# the full commit SHA; without them the probe reports version "dev" and no commit.
ARG IAPSTACK_VERSION=""
ARG IAPSTACK_COMMIT=""
RUN BUILDINFO=github.com/imariman/iapstack/internal/platform/buildinfo \
    && LDFLAGS="-s -w -X ${BUILDINFO}.version=${IAPSTACK_VERSION} -X ${BUILDINFO}.commit=${IAPSTACK_COMMIT}" \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="${LDFLAGS}" -o /out/iapstack ./cmd/iapstack \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/iapstack-webhook-receiver ./cmd/iapstack-webhook-receiver

FROM alpine:3.24

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S -g 65532 iapstack \
    && adduser -S -D -H -u 65532 -G iapstack iapstack
COPY --from=build /out/iapstack /usr/local/bin/iapstack
COPY --from=build /out/iapstack-webhook-receiver /usr/local/bin/iapstack-webhook-receiver

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/iapstack"]
