VERSION 0.8

FROM golang:1.22.5-alpine

# install gcc dependencies into alpine for CGO
RUN apk --no-cache add git ca-certificates gcc musl-dev libc-dev binutils-gold curl openssh

# install docker tools
# https://docs.docker.com/engine/install/debian/
RUN apk add --update --no-cache docker

# install the go generator plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
RUN export PATH="$PATH:$(go env GOPATH)/bin"

# install buf from source
RUN GO111MODULE=on GOBIN=/usr/local/bin go install github.com/bufbuild/buf/cmd/buf@v1.36.0

# install oapi to generate swagger
RUN go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

# install the various tools to generate connect-go
RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
RUN go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

# install linter
# binary will be $(go env GOPATH)/bin/golangci-lint
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.60.1
RUN ls -la $(which golangci-lint)

RUN go install github.com/ory/go-acc@latest

# install vektra/mockery
RUN go install github.com/vektra/mockery/v2@v2.43.2


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
    RUN mockery  --dir hash --name Hasher --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/hash --case snake
    RUN mockery  --dir discovery --name Provider --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/discovery --case snake
    RUN mockery  --dir internal/cluster --name Interface --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/cluster --case snake

    SAVE ARTIFACT ./mocks mocks AS LOCAL mocks


lint:
    FROM +vendor

    COPY .golangci.yml ./

    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m


local-test:
    FROM +vendor

    RUN go-acc ./... -o coverage.out --ignore goaktpb,mocks,internal/internalpb -- -mod=vendor -race -v

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/goakt \
            --path protos/internal \
            --path protos/benchmark \
            --path protos/test

    # save artifact to
    SAVE ARTIFACT gen/goakt AS LOCAL goaktpb
    SAVE ARTIFACT gen/test AS LOCAL  test/data/testpb
    SAVE ARTIFACT gen/internal AS LOCAL internal/internalpb
    SAVE ARTIFACT gen/benchmark AS LOCAL bench/benchmarkpb
