# syntax=docker/dockerfile:1
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# CI type-checks, so skip the tsc step that `npm run build` adds.
RUN npx vite build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /innkeeper ./cmd/innkeeper
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ink ./cmd/ink

# The built-in plugins, built against this core's protocol package. Owners
# install the rest at runtime.
FROM golang:1.27-alpine AS plugins
# A branch or commit. CI passes main's commit, so a new one skips the cached clone.
ARG PLUGINS_REF=main
RUN apk add --no-cache git make
RUN git clone https://github.com/inkandale/innkeeper-plugins.git /plugins-src \
    && git -C /plugins-src checkout --detach "$PLUGINS_REF"
COPY . /core
WORKDIR /plugins-src
# ponytail: no go mod tidy, so this needs innkeeper-plugins' go.sum to cover
# everything protocol/ imports; add tidy if a build reports a missing entry.
RUN go mod edit -replace github.com/inkandale/innkeeper=/core \
    && make install DEST=/plugins PLUGINS="password-login card-codec url-import"

FROM alpine:3.23
# pg_dump for `innkeeper backup`; tini reaps plugin processes whose groups the host kills.
RUN apk add --no-cache postgresql18-client tini \
    && adduser -D -u 10001 innkeeper \
    && mkdir -p /data/blobs /data/plugins \
    && chown -R innkeeper /data
COPY --from=build /innkeeper /usr/local/bin/innkeeper
COPY --from=build /ink /usr/local/bin/ink
COPY --from=plugins /plugins /usr/lib/innkeeper/plugins
USER innkeeper
ENV INNKEEPER_BLOB_DIR=/data/blobs \
    INNKEEPER_PLUGINS_DIR=/data/plugins \
    INNKEEPER_SECRETS_KEY_FILE=/data/secrets.key \
    INNKEEPER_BUILTIN_PLUGINS_DIR=/usr/lib/innkeeper/plugins \
    INNKEEPER_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "innkeeper"]
CMD ["api"]
