# Usage:
#   make showcase-build   # Build the image
#   make showcase-run     # Start the hot-reload watcher with repo mounted
#   make showcase-clean   # Remove the image

FROM golang:1.26-alpine

# Install build/runtime dependencies and git for go install
RUN apk add --no-cache git make bash gcompat libgcc libstdc++ python3

# Install air for hot-reloading
RUN go install github.com/air-verse/air@latest

WORKDIR /workspace

# Copy Go module manifests and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy only the files required for the showcase static generator and dev server
COPY cmd/showcase/ ./cmd/showcase/
COPY cmd/showcase-dev/ ./cmd/showcase-dev/
COPY internal/web/showcase/ ./internal/web/showcase/
COPY .air.showcase.toml ./

EXPOSE 3000

CMD ["air", "-c", ".air.showcase.toml"]
