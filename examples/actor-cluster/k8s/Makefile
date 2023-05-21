.PHONY: cluster-down
example-rm:
	kubectl delete -f ./k8s.yml

.PHONY: cluster-up
example-up:
	minikube image load --overwrite=true accounts:dev
	kubectl apply -f ./k8s.yml
