# How to run it?

1. install [Earthly](https://earthly.dev/get-earthly)
2. install [Minikube](https://minikube.sigs.k8s.io/docs/start/)
3. run `minikube start`
4. clone the repository
5. run at the root of the cloned repository `earthly +k8s-image`
6. run `make cluster-up` to start the cluster. To stop the cluster just run `make cluster-down`
7. run `kubectl port-forward service/accounts 50051:50051`. With any gRPC client you can access the service. The service
   definitions is [here](../../../protos/sample/pb/v1)
