##  Kubernetes Discovery Provider setup

To get the kubernetes discovery working as expected, the following pod labels need to be set:

* `app.kubernetes.io/part-of`: set this label with the actor system name
* `app.kubernetes.io/component`: set this label with the application name
* `app.kubernetes.io/name`: set this label with the application name

In addition, each node _is required to have three different ports open_ with the following ports name for the cluster
engine to work as expected:

* `gossip-port`: help the gossip protocol engine. This is actually the kubernetes discovery port
* `cluster-port`: help the cluster engine to communicate with other GoAkt nodes in the cluster
* `remoting-port`: help for remoting messaging between actors

### Role Based Access

You’ll also have to grant the Service Account that your pods run under access to list pods. The following configuration
can be used as a starting point.
It creates a Role, pod-reader, which grants access to query pod information. It then binds the default Service Account
to the Role by creating a RoleBinding.
Adjust as necessary:

```
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: pod-reader
rules:
  - apiGroups: [""] # "" indicates the core API group
    resources: ["pods"]
    verbs: ["get", "watch", "list"]
---
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: read-pods
subjects:
  # Uses the default service account. Consider creating a new one.
  - kind: ServiceAccount
    name: default
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
```

### Sample Project

A working example can be found [here](./examples/actor-cluster/k8s) with a
small [doc](./examples/actor-cluster/k8s/doc.md) showing how to run it.
