#!/bin/bash

kind create cluster --config kind-config.yaml
kind load docker-image distributed-scheduling:latest --name kind


docker build -t qwednesday/distributed-scheduling .
docker push qwednesday/distributed-scheduling

docker run -d -p 5001:5000 --name registry registry:2


export https_proxy=http://127.0.0.1:7890
export http_proxy=http://127.0.0.1:7890


docker build -t localhost:5001/distributed-scheduling .
docker push localhost:5001/distributed-scheduling:latest

docker tag qwednesday/distributed-scheduling:latest localhost:5001/distributed-scheduling:latest
docker push k


docker network connect "kind" "registry"


kubectl run nginx --image=nginx