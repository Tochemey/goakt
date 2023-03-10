VERSION 0.7
PROJECT tochemey/goakt


FROM tochemey/docker-go:1.19.3-0.5.0

test-pr:
  PIPELINE
  TRIGGER pr main
  BUILD +lint
  BUILD +local-test

# etsts
test-main:
  PIPELINE
  TRIGGER push main
  BUILD +lint
  BUILD +local-test

test:
  PIPELINE
  TRIGGER push main
  TRIGGER pr main
  BUILD +lint
  BUILD +local-test

code:

    WORKDIR /app

    # download deps
    COPY go.mod go.sum ./
    RUN go mod download -x

    # copy in code
    COPY --dir . ./

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

    WITH DOCKER --pull postgres:11
        RUN go test -mod=vendor ./... -race -v -coverprofile=coverage.out -covermode=atomic -coverpkg=./...
    END

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out

internal-pb:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/internal/goakt

    # save artifact to
    SAVE ARTIFACT gen/goakt AS LOCAL internal/goaktpb

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/public/messages

    # save artifact to
    SAVE ARTIFACT gen/messages AS LOCAL messages

testprotogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/test/pb

    # save artifact to
    SAVE ARTIFACT gen gen AS LOCAL test/data

sample-pb:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/sample/pb

    # save artifact to
    SAVE ARTIFACT gen gen AS LOCAL examples/protos


