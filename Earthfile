VERSION 0.8

FROM tochemey/docker-go:1.22.2-3.1.0

RUN go install github.com/ory/go-acc@latest
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
    RUN mockery

    SAVE ARTIFACT ./mocks mocks AS LOCAL mocks


lint:
    FROM +vendor

    COPY .golangci.yml ./

    # Runs golangci-lint with settings:
    RUN golangci-lint run --timeout 10m


local-test:
    FROM +vendor

    RUN go-acc ./... -o coverage.out --ignore goaktpb,examples,mocks,internal/internalpb -- -mod=vendor -race -v

    SAVE ARTIFACT coverage.out AS LOCAL coverage.out

protogen:
    # copy the proto files to generate
    COPY --dir protos/ ./
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/goakt \
            --path protos/internal \
            --path protos/sample \
            --path protos/test

    # save artifact to
    SAVE ARTIFACT gen/goakt AS LOCAL goaktpb
    SAVE ARTIFACT gen/test AS LOCAL  test/data/testpb
    SAVE ARTIFACT gen/sample AS LOCAL examples/protos/samplepb
    SAVE ARTIFACT gen/internal AS LOCAL internal/internalpb


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
    EXPOSE 9092

    ENTRYPOINT ["./accounts"]
    SAVE IMAGE accounts:dev

compile-static:
    COPY +vendor/files ./

    RUN go build -mod=vendor  -o bin/accounts ./examples/actor-cluster/static
    SAVE ARTIFACT bin/accounts /accounts

static-image:
    FROM alpine:3.17

    WORKDIR /app
    COPY +compile-static/accounts ./accounts
    RUN chmod +x ./accounts

    # expose the various ports in the container
    EXPOSE 50051
    EXPOSE 50052
    EXPOSE 3322
    EXPOSE 3320
    EXPOSE 9092

    ENTRYPOINT ["./accounts"]
    SAVE IMAGE accounts:dev
