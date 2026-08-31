# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/iapstack ./cmd/iapstack \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/iapstack-webhook-receiver ./cmd/iapstack-webhook-receiver

FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S -g 65532 iapstack \
    && adduser -S -D -H -u 65532 -G iapstack iapstack
COPY --from=build /out/iapstack /usr/local/bin/iapstack
COPY --from=build /out/iapstack-webhook-receiver /usr/local/bin/iapstack-webhook-receiver

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/iapstack"]
