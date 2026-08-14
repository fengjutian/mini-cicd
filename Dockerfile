FROM node:24-alpine AS web
WORKDIR /src/apps/web
COPY apps/web/package*.json ./
RUN npm ci
COPY apps/web/ ./
RUN npm run build

FROM golang:1.26.6-alpine AS server
ARG VERSION=dev
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY apps/server ./apps/server
COPY --from=web /src/apps/server/internal/server/webdist ./apps/server/internal/server/webdist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/charlesfeng/mini-cicd/apps/server/internal/server.Version=${VERSION}" -o /out/minicicd ./apps/server/cmd/minicicd

FROM alpine:3.22
RUN apk add --no-cache bash ca-certificates git openssh-client tzdata && addgroup -S minicicd && adduser -S -G minicicd -h /var/lib/minicicd minicicd
COPY --from=server /out/minicicd /usr/local/bin/minicicd
RUN mkdir -p /var/lib/minicicd && chown -R minicicd:minicicd /var/lib/minicicd
USER minicicd
ENV MINICICD_LISTEN_ADDR=0.0.0.0:8080 MINICICD_DATA_DIR=/var/lib/minicicd MINICICD_SHELL=/bin/bash
VOLUME ["/var/lib/minicicd"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/minicicd"]
