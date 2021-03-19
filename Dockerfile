# Builder image
FROM golang:1.16-buster AS builder

WORKDIR /go/src/github.com/djmaze/docker-volume-goofys

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -a -v -installsuffix cgo

# Root image
FROM debian

RUN apt-get update \
 && apt-get install -y ca-certificates fuse \
 && rm /var/cache/apt/lists/* -fR

RUN mkdir -p /run/docker/plugins /mnt/state /mnt/volumes

ADD https://github.com/kahing/catfs/releases/download/v0.8.0/catfs /usr/local/bin/catfs
RUN chmod u+x /usr/local/bin/catfs

COPY --from=builder /go/src/github.com/djmaze/docker-volume-goofys/goofys-docker docker-volume-goofys
