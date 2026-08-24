# syntax=docker/dockerfile:1.7
FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/iapstack-e2e-fixture ./cmd/iapstack-e2e-fixture

FROM alpine:3.22

RUN addgroup -S -g 65532 iapstack \
    && adduser -S -D -H -u 65532 -G iapstack iapstack
COPY --from=build /out/iapstack-e2e-fixture /usr/local/bin/iapstack-e2e-fixture

USER 65532:65532
EXPOSE 8082 8443
ENTRYPOINT ["/usr/local/bin/iapstack-e2e-fixture"]
