# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /xuandu ./cmd

FROM alpine:3.23 AS runtime-base
RUN apk add --no-cache ca-certificates && \
    addgroup -g 10001 gateway && adduser -D -u 10001 -G gateway gateway && \
    mkdir -p /etc/xuandu
USER 10001:10001
EXPOSE 8080 8081 9090
ENTRYPOINT ["/usr/local/bin/xuandu"]
CMD ["-config", "/etc/xuandu/gateway.yaml"]

# CI downloads the already embedded binary; restore its executable bit after artifact transfer.
FROM runtime-base AS ci
COPY --chmod=0755 dist/xuandu /usr/local/bin/xuandu

# Default target keeps local builds self-contained, including the frontend.
FROM runtime-base AS runtime
COPY --from=build --chmod=0755 /xuandu /usr/local/bin/xuandu
