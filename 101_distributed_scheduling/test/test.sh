#!/bin/bash
set -e

ROOT=$(unset CDPATH && cd "$(dirname "${BASH_SOURCE[0]}")"/.. && pwd)
echo "switch working directory to $ROOT"
cd "$ROOT"

echo "create a local registry if not exists"
running="$(docker inspect -f '{{.State.Running}}' "registry" 2>/dev/null || true)"
if [ "${running}" != 'true' ]; then
  if [ "${running}" == 'false' ]; then
    docker start registry
  else
    docker run -d -p 5001:5000 --name registry registry:2
  fi
else
  echo "Docker registry already running"
fi

echo "build and push the image to the local registry"
docker build -t localhost:5001/distributed-scheduling .
docker push localhost:5001/distributed-scheduling:latest

echo "create a kind cluster"
export http_proxy=""
export https_proxy=""
kind create cluster --config test/kind-config.yaml --name webhook-demo
docker network connect "kind" "registry" || true

echo "install the webhook"
kubectl apply -f test/manifests.yaml

echo "wait for the webhook to be ready"
kubectl wait --for=condition=ready pod -l app=webhook -n webhook-demo

echo "test1: create a deployment"
kubectl apply -f test/deployment.yaml
kubectl wait --for=jsonpath='{.status.readyReplicas}'=3 deployment/test-deployment

echo "check if there is 1 pod running on on-demand node(kind-worker)"
od_pod=$(kubectl get pods -l app=test-deployment -o yaml | grep nodeName | grep -v webhook-demo-worker2 | wc -l)
if [ "$od_pod" -lt 1 ]; then
    echo "test1 failed"
    echo "there is $od_pod pods running on on-demand node"
    exit 1
fi

echo "scale the deployment to 5"
kubectl scale deployment test-deployment --replicas=5
kubectl wait --for=jsonpath='{.status.readyReplicas}'=5 deployment/test-deployment

echo "check if there are 4 pods running on spot node(worker2)"
spot_pod=$(kubectl get pods -l app=test-deployment -o yaml | grep nodeName | grep  webhook-demo-worker2 | wc -l)
if [ "$spot_pod" -ne 4 ]; then
    echo "test1 failed"
    echo "there are $spot_pod pods running on spot node"
    exit 1
fi

echo "scale the deployment to 1"
kubectl scale deployment test-deployment --replicas=1
kubectl wait --for=jsonpath='{.status.readyReplicas}'=1 deployment/test-deployment

echo "check if there is 1 pod running on on-demand node(worker)"
od_pod=$(kubectl get pods -l app=test-deployment -o yaml | grep nodeName | grep -v webhook-demo-worker2 | wc -l)
if [ "$od_pod" -lt 1 ]; then
    echo "test1 failed"
    echo "there is $od_pod running on on-demand node"
    exit 1
fi

echo "test2: create a statefulset"
kubectl apply -f test/statefulset.yaml
kubectl wait --for=jsonpath='{.status.readyReplicas}'=3 statefulset/test-statefulset

echo "check if there is 2 pods running on on-demand node(worker)"
od_pod=$(kubectl get pods -l app=test-statefulset -o yaml | grep nodeName | grep -v webhook-demo-worker2 | wc -l)
if [ "$od_pod" -lt 2 ]; then
    echo "test2 failed"
    echo "there is $od_pod pods running on on-demand node"
    exit 1
fi

echo "all tests passed"
echo "clean up the kind cluster"
kind delete cluster --name webhook-demo