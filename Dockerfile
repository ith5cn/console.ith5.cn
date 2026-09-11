# syntax=docker/dockerfile:1

FROM node:24-bookworm AS web-build
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-build /src/web/dist ./web/dist
ARG VERSION=dev
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ith5-server ./cmd/ith5-server

FROM debian:13-slim AS runtime
WORKDIR /opt/ith5
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata wget \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-build /out/ith5-server ./ith5-server
ENV ITH5_LISTEN_ADDR=:8080
EXPOSE 8080
CMD ["/opt/ith5/ith5-server"]
