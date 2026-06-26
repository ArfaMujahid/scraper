# Multi-stage build: a few-MB static image with the dashboard assets embedded.

# ---- build ----
FROM golang:1.26 AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO disabled for a fully static binary; trim symbols for size.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/scraper ./cmd/scraper

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /home/nonroot
COPY --from=build /out/scraper /usr/local/bin/scraper

EXPOSE 8080
# Bind to all interfaces inside the container (localhost-only is the default
# elsewhere, per NFR-S4). Output goes to /home/nonroot/data — mount a volume to
# persist it.
ENTRYPOINT ["/usr/local/bin/scraper"]
CMD ["--ui", "--ui-addr=0.0.0.0:8080"]
