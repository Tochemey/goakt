# Kubernetes (kind) reproduction for issue #1260.
#
# Prerequisites:
#   - Docker
#   - Kind: https://kind.sigs.k8s.io
#   - kubectl
#
# Quick start:
#   make cluster-create   # create the kind cluster (one-time)
#   make deploy           # build the image, load it into kind, deploy
#   make demo             # spawn workers, crash a pod, verify full recovery
#   make cluster-delete   # tear everything down
#
# Run from: playground/issue-1260/

.DEFAULT_GOAL := help

CLUSTER_NAME ?= goakt-issue-1260
IMAGE_NAME ?= issue1260:dev
DEPLOY_DIR := ./deploy
REPO_ROOT := ../..

.PHONY: help
help:
	@echo "issue-1260 reproduction - crash recovery on the default cluster configuration"
	@echo ""
	@echo "Targets:"
	@echo "  cluster-create   Create the kind cluster"
	@echo "  cluster-delete   Delete the kind cluster"
	@echo "  image            Build the Docker image and load it into kind"
	@echo "  deploy           Build, load, and deploy the StatefulSet"
	@echo "  demo             Run the self-validating crash-recovery demo"
	@echo "  status           Show pod status"
	@echo "  logs             Tail logs from the pods"
	@echo "  undeploy         Remove the deployment"

.PHONY: cluster-create
cluster-create:
	kind create cluster --name $(CLUSTER_NAME) --config $(DEPLOY_DIR)/kind-config.yaml --wait 2m

.PHONY: cluster-delete
cluster-delete:
	kind delete cluster --name $(CLUSTER_NAME)

.PHONY: image
image:
	docker build -t $(IMAGE_NAME) -f Dockerfile $(REPO_ROOT)
	kind load docker-image $(IMAGE_NAME) --name $(CLUSTER_NAME)

.PHONY: deploy
deploy: image
	kubectl apply -f $(DEPLOY_DIR)/k8s.yaml
	kubectl rollout status sts/issue1260 --timeout=300s

.PHONY: demo
demo:
	./scripts/demo.sh

.PHONY: status
status:
	kubectl get pods -l app.kubernetes.io/name=issue1260 -o wide

.PHONY: logs
logs:
	kubectl logs -l app.kubernetes.io/name=issue1260 --all-containers --prefix -f --tail=50

.PHONY: undeploy
undeploy:
	kubectl delete -f $(DEPLOY_DIR)/k8s.yaml --ignore-not-found
