# syntax=docker/dockerfile:1
#
# Kyurem gateway image. Build from the repository root:
#   docker build -f deploy/docker/gateway.Dockerfile -t kyurem/gateway .
#
# The build stage deliberately copies only the gateway module: the image does
# not need the demo workspace or the root go.work file.

FROM golang:1.26-alpine AS build
WORKDIR /src

COPY gateway/go.mod gateway/go.sum ./
RUN go mod download

COPY gateway/ ./
ARG KYUREM_VERSION=0.1.0-dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X github.com/kyurem-project/kyurem/gateway/internal/version.Version=${KYUREM_VERSION}" \
    -o /out/kyurem ./cmd/kyurem

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S kyurem \
    && adduser -S -G kyurem -H -h /nonexistent kyurem

COPY --from=build /out/kyurem /usr/local/bin/kyurem

USER kyurem
EXPOSE 8080 8081
ENTRYPOINT ["/usr/local/bin/kyurem"]
CMD ["serve", "--config", "/etc/kyurem/config.yaml"]
