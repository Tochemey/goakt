VERSION 0.8

FROM golang:1.26.0-alpine

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
RUN GO111MODULE=on GOBIN=/usr/local/bin go install github.com/bufbuild/buf/cmd/buf@v1.65.0

# install the various tools to generate connect-go
RUN go install github.com/fullstorydev/grpcurl/cmd/grpcurl@latest
RUN go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

# install linter
# binary will be $(go env GOPATH)/bin/golangci-lint
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(go env GOPATH)/bin v2.9.0
RUN golangci-lint --version

# install vektra/mockery
RUN go install github.com/vektra/mockery/v2@v2.53.2


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
    RUN mockery  --dir internal/cluster --name Cluster --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/cluster --case snake
    RUN mockery  --dir extension --name Dependency --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/extension --case snake
    RUN mockery  --dir extension --name Extension --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/extension --case snake
    RUN mockery --dir remote --name Client --keeptree --exported=true --with-expecter=true --inpackage=true --disable-version-string=true --output ./mocks/remote --case snake

    SAVE ARTIFACT ./mocks mocks AS LOCAL mocks


lint:
    FROM +vendor

    COPY .golangci.yml ./

    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m


local-test:
    FROM +vendor

    RUN go test -mod=vendor -p 1 -timeout 0 -race -v -coverprofile=coverage.out -covermode=atomic -coverpkg=./... $(go list -mod=vendor ./... | grep -v -E "(goaktpb|mocks|internal/internalpb)")

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/internal \
            --path protos/test

    # save artifact to
    SAVE ARTIFACT gen/test AS LOCAL  test/data/testpb
    SAVE ARTIFACT gen/internal AS LOCAL internal/internalpb

certs:
    FROM alpine/openssl:3.5.2

    COPY --dir test/data/certs ./certs

    RUN rm certs/*.key || rm certs/*.srl || rm certs/*.csr || rm certs/*.pem || rm certs/*.cert || true
    RUN openssl genrsa -out certs/ca.key 4096
    RUN openssl req -new -x509 -key certs/ca.key -sha256 -subj "/C=US/ST=TX/O=Opensource" -days 3650 -out certs/ca.cert
    RUN openssl genrsa -out certs/auto.key 4096
    RUN openssl req -new -key certs/auto.key -out certs/auto.csr -config certs/auto.conf
    RUN openssl x509 -req -in certs/auto.csr -CA certs/ca.cert -CAkey certs/ca.key -set_serial 1 -out certs/auto.pem -days 3650 -sha256 -extfile certs/auto.conf -extensions req_ext
    RUN openssl genrsa -out certs/auto_no_ip_san.key 4096
    RUN openssl req -new -key certs/auto_no_ip_san.key -out certs/auto_no_ip_san.csr -config certs/auto_no_ip_san.conf
    RUN openssl x509 -req -in certs/auto_no_ip_san.csr -CA certs/ca.cert -CAkey certs/ca.key -set_serial 2 -out certs/auto_no_ip_san.pem -days 3650 -sha256 -extfile certs/auto_no_ip_san.conf -extensions req_ext

    # Client Auth
    RUN openssl req -new -x509 -days 3650 -keyout certs/client-auth-ca.key -out certs/client-auth-ca.pem -subj "/C=TX/ST=TX/O=Opensource/CN=auto.io/emailAddress=admin@auto-rpc.org" -passout pass:test
    RUN openssl genrsa -out certs/client-auth.key 2048
    RUN openssl req -sha1 -key certs/client-auth.key -new -out certs/client-auth.req -subj "/C=US/ST=TX/O=Opensource/CN=client.com/emailAddress=admin@auto-rpc.org"
    RUN openssl x509 -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -set_serial 3 -passin pass:test -out certs/client-auth.pem
    RUN openssl x509 -extfile certs/client-auth.conf -extensions ssl_client -req -days 3650 -in certs/client-auth.req -CA certs/client-auth-ca.pem -CAkey certs/client-auth-ca.key -set_serial 4 -passin pass:test -out certs/client-auth.pem


    SAVE ARTIFACT certs AS LOCAL test/data/certs
