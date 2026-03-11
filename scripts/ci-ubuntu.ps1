$ErrorActionPreference = "Stop"

$image = "conf-ci-ubuntu"

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw "docker is not installed or not on PATH."
}

docker version | Out-Null

docker build -f docker/ubuntu-ci.Dockerfile -t $image .
docker run --rm $image
