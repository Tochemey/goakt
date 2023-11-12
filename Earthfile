VERSION 0.7

FROM tochemey/docker-go:1.21.0-1.0.0

# install the various tools to generate connect-go
RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
RUN go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest
RUN apk --no-cache add git ca-certificates gcc musl-dev libc-dev binutils-gold

pbs:
    BUILD +protogen
    BUILD +sample-pb
    BUILD +testprotogen

test:
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

    COPY +mock/mocks ./mocks

    RUN go mod tidy && go mod vendor
    SAVE ARTIFACT /app /files


mock:
    # copy in the necessary files that need mock generated code
    FROM +code

    # generate the mocks
    RUN mockery  --all --dir hash --recursive --keeptree --exported=true --with-expecter=true --inpackage=true --output ./mocks/hash --case snake
    RUN mockery  --all --dir pkg --recursive --keeptree --exported=true --with-expecter=true --inpackage=true --output ./mocks/pkg --case snake
    RUN mockery  --all --dir internal --recursive --keeptree --exported=true --with-expecter=true  --inpackage=true --output ./mocks/internal --case snake
    RUN mockery  --all --dir discovery --recursive --keeptree --exported=true --with-expecter=true --inpackage=true --output ./mocks/discovery --case snake
    RUN mockery  --all --dir cluster --recursive --keeptree --exported=true --with-expecter=true --inpackage=true --output ./mocks/cluster --case snake

    SAVE ARTIFACT ./mocks mocks AS LOCAL testkit


lint:
    FROM +vendor

    COPY .golangci.yml ./

    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m


local-test:
    FROM +vendor

    RUN go test -mod=vendor ./... -race -v -coverprofile=coverage.out -covermode=atomic -coverpkg=./...

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/goakt/address \
            --path protos/goakt/messages \
            --path protos/goakt/events \
            --path protos/goakt/internal

    # save artifact to
    SAVE ARTIFACT gen/address AS LOCAL pb/address
    SAVE ARTIFACT gen/messages AS LOCAL pb/messages
    SAVE ARTIFACT gen/events AS LOCAL pb/events
    SAVE ARTIFACT gen/internal AS LOCAL internal

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

compile-k8s:
    COPY +vendor/files ./

    RUN go build -mod=vendor  -o bin/accounts ./examples/actor-cluster/k8s
    SAVE ARTIFACT bin/accounts /accounts

k8s-image:
    FROM alpine:3.17

    WORKDIR /app
    COPY +compile-k8s/accounts ./accounts
    RUN chmod +x ./accounts

    # expose the various ports in the container
    EXPOSE 50051
    EXPOSE 50052
    EXPOSE 3322
    EXPOSE 3320

    ENTRYPOINT ["./accounts"]
    SAVE IMAGE accounts:dev


compile-dnssd:
    COPY +vendor/files ./

    RUN go build -mod=vendor  -o bin/accounts ./examples/actor-cluster/dnssd
    SAVE ARTIFACT bin/accounts /accounts

dnssd-image:
    FROM alpine:3.17

    WORKDIR /app
    COPY +compile-dnssd/accounts ./accounts
    RUN chmod +x ./accounts

    # expose the various ports in the container
    EXPOSE 50051
    EXPOSE 50052
    EXPOSE 3322
    EXPOSE 3320

    ENTRYPOINT ["./accounts"]
    SAVE IMAGE accounts:dev
