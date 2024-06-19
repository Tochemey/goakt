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
    COPY buf.work.yaml buf.gen.yaml ./

    # generate the pbs
    RUN buf generate \
            --template buf.gen.yaml \
            --path protos/goakt \
            --path protos/internal \
            --path protos/test

    # save artifact to
    SAVE ARTIFACT gen/goakt AS LOCAL goaktpb
    SAVE ARTIFACT gen/test AS LOCAL  test/data/testpb
    SAVE ARTIFACT gen/internal AS LOCAL internal/internalpb