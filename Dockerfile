FROM golang:1.26-bookworm AS builder

WORKDIR /app

ENV CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=amd64

RUN apt-get update \
 && apt-get install -y --no-install-recommends zlib1g-dev \
 && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags "-s -w" -o /out/tgmultibot .

FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/tgmultibot /app/bin/tgmultibot

CMD ["bin/tgmultibot"]
