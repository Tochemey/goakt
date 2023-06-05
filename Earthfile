VERSION 0.7
PROJECT tochemey/goakt


FROM tochemey/docker-go:1.20.4-0.8.0

# install the various tools to generate connect-go
RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
RUN go install github.com/bufbuild/connect-go/cmd/protoc-gen-connect-go@latest

pbs:
    BUILD +internal-pb
    BUILD +protogen
    BUILD +sample-pb

test:
  #BUILD +lint
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

    RUN go mod vendor
    SAVE ARTIFACT /app /files


mock:
    # copy in the necessary files that need mock generated code
    FROM +code

    # generate the mocks
    RUN mockery  --all --dir pkg --recursive --keeptree --exported=true --with-expecter=true --output ./mocks/pkg --case snake
    RUN mockery  --all --dir internal --recursive --keeptree --exported=true --with-expecter=true --output ./mocks/internal --case snake
    RUN mockery  --all --dir discovery --recursive --keeptree --exported=true --with-expecter=true --output ./mocks/discovery --case snake

    SAVE ARTIFACT ./mocks mocks AS LOCAL mocks


lint:
    FROM +vendor

    COPY .golangci.yml ./

    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m


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
    SAVE ARTIFACT gen/goakt AS LOCAL internal/goakt

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

compile-actor-cluster:
    COPY +vendor/files ./

    RUN go build -mod=vendor  -o bin/accounts ./examples/actor-cluster/k8s
    SAVE ARTIFACT bin/accounts /accounts

actor-cluster-image:
    FROM alpine:3.16.2

    WORKDIR /app
    COPY +compile-actor-cluster/accounts ./accounts
    RUN chmod +x ./accounts

    EXPOSE 50051
    EXPOSE 9000
    EXPOSE 2379
    EXPOSE 2380

    ENTRYPOINT ["./accounts"]
    SAVE IMAGE accounts:dev
