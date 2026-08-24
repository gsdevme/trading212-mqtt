# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26 AS build
WORKDIR /src

# Cache dependencies.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags "-s -w" \
    -o /out/trading212-mqtt ./cmd

# --- runtime stage ---
FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=build /out/trading212-mqtt /trading212-mqtt

# Production default: the real API. The service is stateless and writes nothing
# to disk, so the container can run with a read-only root filesystem.
ENV MODE=live
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/trading212-mqtt"]
CMD ["serve"]
