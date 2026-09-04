# Docker Compose reproduction for issue #1340.
#
# Prerequisites:
#   - Docker and Docker Compose
#   - jq or python3 (the demo reads the JSON reports with whichever is present)
#
# Quick start:
#   make build   # build the image from the local working tree
#   make demo    # run the self-validating kill and recovery measurement
#   make clean   # remove the containers, the network and the image
#
# Run from: playground/issue-1340/

.DEFAULT_GOAL := help

IMAGE_NAME ?= issue1340:dev

.PHONY: help
help:
	@echo "issue-1340 reproduction - how fast a killed node is reported gone and its singleton comes back"
	@echo ""
	@echo "Targets:"
	@echo "  build                Build the reproduction image from the repository root"
	@echo "  up                   Start the three nodes and the DNS server"
	@echo "  down                 Stop everything and remove the volumes"
	@echo "  demo                 Run the self-validating kill and recovery measurement"
	@echo "  demo-short-timeout   Same measurement with a 5s convergence timeout"
	@echo "  demo-local-profile   Same measurement with the Local network profile"
	@echo "  compare              Run the three scenarios and print the comparison table"
	@echo "  logs                 Follow the logs of the three nodes"
	@echo "  clean                Run down and remove the image"

.PHONY: build
build:
	docker compose build

.PHONY: up
up:
	docker compose up -d

.PHONY: down
down:
	docker compose down -v --remove-orphans

.PHONY: demo
demo:
	./scripts/demo.sh

# The two settings the scenarios vary. Lowering the convergence timeout must
# not move NodeLeft, because a departure does not wait on it; the network
# profile must, because it sets how quickly a failure is confirmed.
.PHONY: demo-short-timeout
demo-short-timeout:
	SCENARIO=short-timeout CONVERGENCE_TIMEOUT=5s ./scripts/demo.sh

.PHONY: demo-local-profile
demo-local-profile:
	SCENARIO=local-profile NETWORK_PROFILE=local ./scripts/demo.sh

.PHONY: compare
compare:
	./scripts/compare.sh

.PHONY: logs
logs:
	docker compose logs -f --tail=100 node1 node2 node3

.PHONY: clean
clean: down
	-docker image rm -f $(IMAGE_NAME)
