# GPU Optimization

## General Ideas

We all have experience with scheduling non-GPU pods. GPUs, like CPUs and memory, can be abstracted as a resource. My approach is to first transform GPU management into something similar to CPUs and memory, and then leverage the experience of scheduling non-GPU pods.

### 1. GPU sharing

We want to enable multiple Pods to use a single GPU simultaneously. This requires virtualizing the GPU. Nvidia GPUs support different methods, including [Time-Slicing](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-sharing.html), [MPS](https://docs.nvidia.com/deploy/mps/), and [MIG](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-operator-mig.html).

With [NVIDIA GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/overview.html) installed, we can easily add resources requests to Pods to schedule them on Nodes with GPU. And Pods running on the same Node can share the GPU.

I also find a project that supports advanced GPU sharing. [nos](https://nebuly-ai.github.io/nos/overview/) supports dynamic GPU partitioning. The GPU partitioning is performed automatically in real-time based on Pods pending and running in the cluster, which can help improve the efficiency of GPU utilization.

### 2. Node provisioning

If the Nodes with GPU are not enough in the cluster, we must provision new Nodes. However, if many GPU nodes are idle, it will cause huge waste. We should automatically remove these nodes. We can use [Karpenter](https://karpenter.sh/) to do the auto-provisioning.

### 3. Pod scheduling

As I know, there are spot nodes with GPU, which are much cheaper than on-demand nodes. For example, AWS g4ad.xlarge instance with Linux OS: spot instance price is $0.1187 while on-demand instance price is $0.37853. In addition, many machine learning frameworks support resuming training from the last checkpoint. If we schedule a training job to a spot node, and the job is interrupted, we are able to resume the training from the last checkpoint. So I think it's acceptable to schedule some pods to spot nodes with GPU, which can save the cost.

### Summary

From the above 3 aspects, we can optimize the GPU utilization on K8s. Some existing open-source projects can help improve the efficiency of GPU utilization. We can use auto-provisioning framework to automatically recycle the idle GPU nodes to save cost. We can also try to schedule some pods on spot nodes to save cost.

## Rough implementation approach

1. Install NVIDIA GPU Operator and nos to enable dynamic GPU partitioning in the cluster.
2. Install Karpenter to automatically provision GPU nodes.
3. Implement a customized scheduler, which supports configurable scheduling strategies:
   - For important pods, schedule them to on-demand Nodes. And prioritize scheduling them to the same pod to avoid idle GPU resources.
   - Some pods with specific configuration can be scheduled to spot Nodes to save cost.
   - For Job pods that are running on spot Nodes, if they are interrupted, we should have a mechanism to resume it.

## References

- [k8s-device-plugin](https://github.com/NVIDIA/k8s-device-plugin)
- [mGPU 技术揭秘 ：新一代 Kubernetes GPU 共享调度方案](https://developer.volcengine.com/articles/7317093341425434674)
- [gpushare-device-plugin](https://github.com/AliyunContainerService/gpushare-device-plugin)
- [k8s-vgpu-scheduler](https://github.com/4paradigm/k8s-vgpu-scheduler/blob/master/README.md)
- [Intelligent Cloud — Part 3: Optimizing GPU Costs by Leveraging Spot Instances](https://medium.com/lunit/optimizing-gpu-costs-by-leveraging-spot-instances-189e5dfc17ee)
- [nos](https://nebuly-ai.github.io/nos/overview/)
- [Karpenter](https://karpenter.sh/)
