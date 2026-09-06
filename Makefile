# All targets run inside a Docker image built from Dockerfile.tools, so
# contributors only need Docker + Make. No local installs of Go, buf, mockery,
# golangci-lint, openssl, etc. are required, and nothing is cached on the host:
# tool state persists in a Docker volume, test runs start from nothing.

IMAGE       := goakt-tools
WORKDIR     := /src
DOCKER_SOCK ?= /var/run/docker.sock

UID := $(shell id -u)
GID := $(shell id -g)

# Build and module caches persist in a named volume so lint and code
# generation stay fast across runs.
TOOLS_VOLUME := goakt-tools-cache
DOCKER_RUN := docker run --rm \
	--user $(UID):$(GID) \
	-v $(TOOLS_VOLUME):/cache \
	-e HOME=/cache/home \
	-e GOCACHE=/cache/go-build \
	-e GOMODCACHE=/cache/mod \
	-v "$(CURDIR)":$(WORKDIR) \
	-w $(WORKDIR) \
	$(IMAGE)

.PHONY: help image test lint unit-test mock protogen proto-format proto-lint certs vendor clean

help: ## Show available targets
	@awk 'BEGIN{FS=":.*?## "} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

image: ## Build the tools Docker image
	docker build -t $(IMAGE) -f Dockerfile.tools .

test: lint unit-test ## Run lint and unit tests

vendor: image ## Refresh vendored modules (go mod tidy && go mod vendor)
	$(DOCKER_RUN) sh -c 'GOFLAGS= go mod tidy && GOFLAGS= go mod vendor'

lint: image ## Run golangci-lint
	$(DOCKER_RUN) golangci-lint run --timeout 10m

unit-test: image ## Run the test suite as the CI shards in parallel pristine containers (SHARD=<name> runs one, V=1 streams every test)
	@IMAGE=$(IMAGE) DOCKER_SOCK=$(DOCKER_SOCK) SHARD=$(SHARD) V=$(V) scripts/unit-test.sh

mock: image ## Regenerate mocks (config in .mockery.yml)
	$(DOCKER_RUN) mockery

protogen: image ## Regenerate protobuf Go code
	$(DOCKER_RUN) sh -c '\
		rm -rf gen && \
		buf generate --template buf.gen.yaml --path protos/internal --path protos/test && \
		rm -rf test/data/testpb internal/internalpb && \
		mkdir -p test/data && \
		mv gen/test test/data/testpb && \
		mv gen/internal internal/internalpb && \
		rm -rf gen'

proto-format: image ## Format proto files with buf
	$(DOCKER_RUN) buf format -w

proto-lint: image ## Lint proto files with buf
	$(DOCKER_RUN) buf lint

certs: image ## Regenerate TLS test fixtures under test/data/certs
	$(DOCKER_RUN) sh -c 'cd test/data/certs && \
		rm -f *.srl *.csr auto.key auto.pem auto_no_ip_san.key auto_no_ip_san.pem ca.cert ca.key client-auth*.key client-auth*.pem client-auth*.req && \
		openssl genrsa -out ca.key 4096 && \
		openssl req -new -x509 -key ca.key -sha256 -subj "/C=US/ST=TX/O=Opensource" -days 3650 -out ca.cert && \
		openssl genrsa -out auto.key 4096 && \
		openssl req -new -key auto.key -out auto.csr -config auto.conf && \
		openssl x509 -req -in auto.csr -CA ca.cert -CAkey ca.key -set_serial 1 -out auto.pem -days 3650 -sha256 -extfile auto.conf -extensions req_ext && \
		openssl genrsa -out auto_no_ip_san.key 4096 && \
		openssl req -new -key auto_no_ip_san.key -out auto_no_ip_san.csr -config auto_no_ip_san.conf && \
		openssl x509 -req -in auto_no_ip_san.csr -CA ca.cert -CAkey ca.key -set_serial 2 -out auto_no_ip_san.pem -days 3650 -sha256 -extfile auto_no_ip_san.conf -extensions req_ext && \
		openssl req -new -x509 -days 3650 -keyout client-auth-ca.key -out client-auth-ca.pem -subj "/C=TX/ST=TX/O=Opensource/CN=auto.io/emailAddress=admin@auto-rpc.org" -passout pass:test && \
		openssl genrsa -out client-auth.key 2048 && \
		openssl req -sha1 -key client-auth.key -new -out client-auth.req -subj "/C=US/ST=TX/O=Opensource/CN=client.com/emailAddress=admin@auto-rpc.org" && \
		openssl x509 -req -days 3650 -in client-auth.req -CA client-auth-ca.pem -CAkey client-auth-ca.key -set_serial 3 -passin pass:test -out client-auth.pem && \
		openssl x509 -extfile client-auth.conf -extensions ssl_client -req -days 3650 -in client-auth.req -CA client-auth-ca.pem -CAkey client-auth-ca.key -set_serial 4 -passin pass:test -out client-auth.pem'

clean: ## Remove the tools image and its cache volume
	docker rmi -f $(IMAGE)
	docker volume rm -f $(TOOLS_VOLUME)
