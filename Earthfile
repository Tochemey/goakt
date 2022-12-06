VERSION 0.6

FROM tochemey/docker-go:1.19.3-0.5.0


test:
    BUILD +lint
    BUILD +local-test

code:

    WORKDIR /app

    # download deps
    COPY go.mod go.sum ./
    RUN go mod download -x

    # copy in code
    COPY --dir +protogen/gen ./
    COPY --dir actors ./
    COPY --dir log ./
    COPY --dir config ./
    COPY --dir telemetry ./
    COPY --dir cluster ./


vendor:
    FROM +code

    RUN go mod vendor
    SAVE ARTIFACT /app /files

lint:
    FROM +vendor

    COPY .golangci.yml ./
    # Runs golangci-lint with settings:
    RUN golangci-lint run


local-test:
    FROM +vendor

    RUN go test -mod=vendor ./... -race -v -coverprofile=coverage.out -covermode=atomic -coverpkg=./...

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out
    SAVE IMAGE --push ghcr.io/tochemey/goakt-cache:test

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/internal/actors

    # save artifact to
    SAVE ARTIFACT gen gen AS LOCAL gen
