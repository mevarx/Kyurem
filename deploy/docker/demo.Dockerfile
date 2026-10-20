# syntax=docker/dockerfile:1
#
# Demo agent/client image for the compose stack. One image carries all three
# binaries; each compose service selects one via `command`.
# Build from the repository root:
#   docker build -f deploy/docker/demo.Dockerfile -t kyurem/demo .

FROM golang:1.26-alpine AS build
WORKDIR /src

COPY demo/go.mod demo/go.sum ./
RUN go mod download

COPY demo/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/go-echo ./agents/go-echo \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/go-upper ./agents/go-upper \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
        -o /out/demo-client ./scenarios/client

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S kyurem \
    && adduser -S -G kyurem -H -h /nonexistent kyurem

COPY --from=build /out/go-echo /usr/local/bin/go-echo
COPY --from=build /out/go-upper /usr/local/bin/go-upper
COPY --from=build /out/demo-client /usr/local/bin/demo-client

USER kyurem
ENTRYPOINT ["/usr/local/bin/go-echo"]
