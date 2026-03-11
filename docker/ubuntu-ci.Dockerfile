FROM golang:1.25.5-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    gcc \
    git \
    g++ \
    make \
 && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 1000 runner

ENV CGO_ENABLED=1 \
    GOBIN=/usr/local/bin

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.8.0

COPY . .
RUN mkdir -p /home/runner/.cache/go-build && chown -R runner:runner /home/runner /src

USER runner
ENV HOME=/home/runner \
    GOCACHE=/home/runner/.cache/go-build

CMD ["bash", "./scripts/ci-ubuntu.sh"]
