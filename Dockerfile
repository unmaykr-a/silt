# syntax=docker/dockerfile:1

# Frontend: built ONCE on the native builder — the output is arch-independent.
# Never let buildx emulate this stage; a Node build under QEMU takes ten-plus
# minutes and sometimes just dies. See PROJECT.md Section 12.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /w
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Backend: also the native builder, with Go cross-compiling to the target.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /s
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /w/dist ./internal/web/dist
ARG TARGETOS TARGETARCH VERSION=dev
# CGO_ENABLED=0 is non-negotiable: the moment cgo is in the build, arm64
# cross-compilation needs a C toolchain and this Dockerfile stops working.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /silt ./cmd/silt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /silt /silt
EXPOSE 8080
ENTRYPOINT ["/silt"]
