# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /innkeeper ./cmd/innkeeper

# The bundled plugins, built against this core's protocol package.
FROM golang:1.27-alpine AS plugins
ARG PLUGINS_REF=ad5ef1c87eb88535952b6a2630b9cd2d8255153b
RUN apk add --no-cache git make openssh-client \
    && mkdir -p -m 700 /root/.ssh \
    && echo 'github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl' > /root/.ssh/known_hosts
RUN --mount=type=ssh git clone git@github.com:davidgibbons/innkeeper-plugins.git /plugins-src \
    && git -C /plugins-src checkout --detach "$PLUGINS_REF"
COPY . /core
WORKDIR /plugins-src
# ponytail: no go mod tidy, so this needs innkeeper-plugins' go.sum to cover
# everything protocol/ imports; add tidy if a build reports a missing entry.
RUN go mod edit -replace github.com/davidgibbons/innkeeper=/core \
    && make install DEST=/plugins

FROM alpine:3.23
# pg_dump for `innkeeper backup`; tini reaps plugin processes whose groups the host kills.
RUN apk add --no-cache postgresql18-client tini \
    && adduser -D -u 10001 innkeeper \
    && mkdir -p /data/blobs /plugins \
    && chown innkeeper /data/blobs
COPY --from=build /innkeeper /usr/local/bin/innkeeper
COPY --from=plugins /plugins /plugins
USER innkeeper
ENV INNKEEPER_BLOB_DIR=/data/blobs \
    INNKEEPER_PLUGINS_DIR=/plugins \
    INNKEEPER_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "innkeeper"]
CMD ["api"]
