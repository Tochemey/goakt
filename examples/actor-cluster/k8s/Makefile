.PHONY: cluster-down
cluster-down:
	kubectl delete -f ./k8s.yaml

.PHONY: cluster-up
cluster-up:
	minikube image load --overwrite=true accounts:dev
	kubectl apply -f ./k8s.yaml
