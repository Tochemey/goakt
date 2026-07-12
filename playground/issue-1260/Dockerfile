# Build context is the goakt repository root: the reproduction is part of the
# goakt module and builds against the local working tree (vendored deps), so
# the image always tests the checked-out code, not a released version.
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o /issue1260 ./playground/issue-1260

FROM alpine:3.20
COPY --from=builder /issue1260 /issue1260
ENTRYPOINT ["/issue1260"]
