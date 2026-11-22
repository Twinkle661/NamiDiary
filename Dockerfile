FROM golang:1.25-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends libsqlite3-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=1 go build -tags sqlite_fts5 -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/namidiary ./cmd/namidiary

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata && rm -rf /var/lib/apt/lists/* && mkdir -p /app/data && chown 1000:100 /app/data
WORKDIR /app
COPY --from=build /out/namidiary /app/namidiary
USER 1000:100
EXPOSE 8188
ENTRYPOINT ["/app/namidiary"]
