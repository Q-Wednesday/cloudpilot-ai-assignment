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
And for the rest of the Pods, we should schedule them to spot nodes as many as possible.

To schedule specific number of Pods to on-demand nodes, we can implement a webhook for Pods that checks the Deployment which the Pod belongs to when a new Pod is created. If the Deployment does not have enough Pods to run on on-demand nodes, inject a `requiredDuringSchedulingIgnoredDuringExecution` rule to force the Pod to be scheduled on an on-demand node. Otherwise, the `preferredDuringSchedulingIgnoredDuringExecution` rule is injected to schedule the Pod on a spot node. Here are the rules:

```yaml
# Inject following rules to the Pods that we want to schedule on on-demand nodes
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
          - key: node.kubernetes.io/capacity
            operator: In
            values:
              - on-demand

# Inject following rules to the Pods that we want to schedule on spot nodes
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

> [!NOTE]
> When using webhooks to perform real-time pod mutations, a large number of Pods may be created simultaneously with the Deployment. This concurrency may cause inaccurate counts when querying the number of Pods running on on-demand nodes. However, this inaccuracy is typically within three pods, which is tolerable.

When HPA is deployed, the replicas of Deployment may be automatically reduced. To ensure that Pods running on on-demand nodes are not deleted first when scaling down, we can inject the annotation `controller.kubernetes.io/pod-deletion-cost=1` into to Pods that we want to schedule on on-demand nodes.

### StatefulSet (multiple replicas)

StatefulSet is different from Deployment. For most of the services that use StatefulSet, we should make sure that most of the Pods are available.
For example, if we are deploying an ETCD cluster with 2N+1 replicas, we should make sure that the number of available Pods is at least N+1.

The same as Deployment, we should allow the users to configure the number of minimum available replicas. We must ensure that these number of Pods are running on on-demand nodes. Assume that the replicas of the StatefulSet is N, and the minimum available replicas is M, and N/2 < M < N.
Pods of StatefulSet are created with a sequence number, we can simply schedule the first M Pods to on-demand nodes, and the rest are preferred to schedule to spot nodes.

For `pod-0` to `pod-${M-1}`, we should specify a `requiredDuringSchedulingIgnoredDuringExecution` rule to schedule them to on-demand nodes. For `pod-${M}` to `pod-${N-1}`, we can use following rules:

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

The above implementation is simple, which only requires to inject the affinity rules into the Pod spec. It does not require to change the scheduler or implement a new controller.
The strategy above can ensure that the minimum available replicas are running on on-demand nodes.

An alternative implementation for Deployment: We can combine `nodeAffinity` and `topologySpreadConstraints` to achieve this. Assume that the replicas of the Deployment is N, and the minimum available replicas is M, we can set the `preferredDuringSchedulingIgnoredDuringExecution` rule to schedule Pods to spot nodes as many as possible. At the same time, we should set a topologySpreadConstraints with `maxSkew=N-2M` (Since we want to maximize the use of spot nodes, I assume that N>2M).

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

However, this implementation will cause problems when HPA is deployed. For example, when creating a Deployment with `replicas=2` and `minAvailable=1`, `maxSkew` is set to 1.
Initially, there will be 1 pod running on on-demand, another one running on spot. If HPA scales the deployment to `replicas=99`, then there will be 49 pods running on on-demand. But users only want to ensure that one pod runs on on-demand.
Running 49 pods on on-demand will result in too high a cost.
Therefore, we cannot adopt the solution of using `topologySpreadConstraints`.

