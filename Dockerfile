# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /innkeeper ./cmd/innkeeper

FROM alpine:3.23
# pg_dump for `innkeeper backup`; tini reaps plugin processes whose groups the host kills.
RUN apk add --no-cache postgresql18-client tini \
    && adduser -D -u 10001 innkeeper \
    && mkdir -p /data/blobs /plugins \
    && chown innkeeper /data/blobs
COPY --from=build /innkeeper /usr/local/bin/innkeeper
USER innkeeper
ENV INNKEEPER_BLOB_DIR=/data/blobs \
    INNKEEPER_PLUGINS_DIR=/plugins \
    INNKEEPER_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "innkeeper"]
CMD ["api"]
