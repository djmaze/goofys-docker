# Builder image
FROM golang:1.8-alpine AS builder

WORKDIR /go/src/github.com/djmaze/docker-volume-goofys

RUN apk add --no-cache gcc git libc-dev

RUN go get github.com/Masterminds/glide
COPY glide.lock glide.yaml ./
RUN glide i

COPY . .
RUN go build -a -v -installsuffix cgo

# Root image
FROM alpine

RUN apk add --no-cache fuse

RUN mkdir -p /run/docker/plugins /mnt/state /mnt/volumes

COPY --from=builder /go/src/github.com/djmaze/docker-volume-goofys/docker-volume-goofys docker-volume-goofys
