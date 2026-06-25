# TODO (build step 14): minimal multi-stage build.
#   Stage 1 — golang image: CGO_ENABLED=0 go build -o /scraper ./cmd/scraper
#   Stage 2 — distroless/scratch base: copy the static binary (dashboard assets
#             are embedded via go:embed), expose the UI port, set ENTRYPOINT.
