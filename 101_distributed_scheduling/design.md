# Design Document for Distributed Scheduling

## Problem statement

* There are on-demand nodes and spot nodes in a k8s cluster. They are labeled with `node.kubernetes.io/capacity: on-demand` and `node.kubernetes.io/capacity: spot`.
* There are some workloads running on the cluster. They are StatefulSet or Deployment.
* Under the premise of ensuring the normal service of workloads, schedule the workload pods to spot nodes to save the cost.

## Strategy

For different types of workloads, we should use different strategies.

### Single Replica Workload

Whether it's a Deployment or a StatefulSet, if the workload has only one instance, we must ensure that instance is always running. For such workloads, we need to configure a `nodeSelector` to schedule the pod to an on-demand node.

### Deployment (multiple replicas)

We should allow the users to configure the number of minimum available replicas. We must ensure that these number of Pods are running on on-demand nodes.
We can combine `nodeAffinity` and `topologySpreadConstraints` to achieve this. Assume that the replicas of the Deployment is N, and the minimum available replicas is M, we can set the `preferredDuringSchedulingIgnoredDuringExecution` rule to schedule Pods to spot nodes as many as possible. At the same time, we should set a topologySpreadConstraints with `maxSkew=N-2M` (Since we want to maximize the use of spot nodes, I assume that N>2M).

For example, if the replicas is 5, and the minimum available is 2, we can use following rules:

```yaml
affinity:
  nodeAffinity:
    # we prefer to schedule pods to spot nodes
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      preference:
        matchExpressions:
          - key: node.kubernetes.io/capacity
            operator: In
            values:
              - spot
topologySpreadConstraints:
  - maxSkew: 1 #5-2*2, we expect to have 3 pods on spot nodes and 2 on on-demand nodes
    topologyKey: node.kubernetes.io/capacity
    whenUnsatisfiable: DoNotSchedule
    labelSelector:
      matchLabels:
        app: my-app
```

### StatefulSet (multiple replicas)

StatefulSet is different from Deployment. For most of the services that use StatefulSet, we should make sure that most of the Pods are available.
For example, if we are deploying an ETCD cluster with 2N+1 replicas, we should make sure that the number of available Pods is at least N+1.

The same as Deployment, we should allow the users to configure the number of minimum available replicas. We must ensure that these number of Pods are running on on-demand nodes. Assume that the replicas of the StatefulSet is N, and the minimum available replicas is M, and N/2 < M < N.
Pods of StatefulSet are created with a sequence number, we can simply schedule the first M Pods to on-demand nodes, and the rest are preferred to schedule to spot nodes.

For `pod-0` to `pod-${M-1}`, we should specify a `nodeSelector` to schedule them to on-demand nodes. For `pod-${M}` to `pod-${N-1}`, we can use following rules:

```yaml
affinity:
  nodeAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      preference:
        matchExpressions:
          - key: node.kubernetes.io/capacity
            operator: In
            values:
              - spot
```

## Implementation

We can implement a simple mutating webhook to inject the affinity rules into the Pod spec.
When users create Deployment/StatefulSet in the cluster, they can use `distributed-scheduling/min-available` annotation to specify the number of minimum available replicas.
Depending on the type of the workload, replicas and min-available, we can inject the affinity rules into the Pod spec.

## Design justification and alternative implementation

The above implementation is simple, which only requires to inject the affinity rules into the Pod spec. It just use some configurations of `affinity`, `nodeSelector` and `topologySpreadConstraints` to control the scheduling behavior. It does not require to change the scheduler or implement a new controller.
The strategy above can ensure that the minimum available replicas are running on on-demand nodes.

An alternative implementation is to implement a new controller. With the controller we can watch the Deployment/StatefulSet and do more fine-grained scheduling. For example, we can have a cold backup for Deployment, when we watch Pods of the Deployment have received the termination signal, we can scale up the backup Deployment to the minimum available replicas.
