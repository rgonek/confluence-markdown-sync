FROM golang:1.25.5-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    gcc \
    git \
    g++ \
    make \
 && rm -rf /var/lib/apt/lists/*

ENV PATH="/root/go/bin:${PATH}" \
    CGO_ENABLED=1

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@v2.8.0

COPY . .

CMD ["bash", "./scripts/ci-ubuntu.sh"]
