# apply イメージ (FROM scratch に静的バイナリ /apply だけを置く)。
# 利用側の Dockerfile.devcontainer が、このイメージを digest で参照する。
FROM --platform=$BUILDPLATFORM golang:1.24 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /apply .

FROM scratch
COPY --from=build /apply /apply
