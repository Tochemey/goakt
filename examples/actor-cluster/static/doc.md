# How to run it?

1. install [Earthly](https://earthly.dev/get-earthly)
2. clone the repository
3. run at the root of the cloned repository `earthly +static-image`
4. run `docker-compose up node1 node2 node3` to start the cluster of three nodes. To stop the cluster just run `docker-compose down -v --remove-orphans`
5. run `docker-compose ps` to list the running instances and their exposed ports to use by any grpc client. With any gRPC client you can access the service. The service
   definitions is [here](../../../protos/sample/pb/v1)
