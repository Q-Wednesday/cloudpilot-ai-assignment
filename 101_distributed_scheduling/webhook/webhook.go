package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

var (
	scheme    = runtime.NewScheme()
	codecs    = serializer.NewCodecFactory(scheme)
	clientset *kubernetes.Clientset
)

const (
	workloadTypeStandalone  = "Standalone"
	workloadTypeDeployment  = "Deployment"
	workloadTypeStatefulSet = "StatefulSet"
)

type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func init() {
	var err error
	clientset, err = getClient()
	if err != nil {
		panic(err)
	}
}

func ServeMutate(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil {
		klog.Error("empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Error("read body failed: ", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var admissionReview admissionv1.AdmissionReview
	var admissionResp *admissionv1.AdmissionResponse
	_, _, err = codecs.UniversalDeserializer().Decode(body, nil, &admissionReview)
	if err != nil {
		klog.Error("decode body failed: ", err)
		admissionResp = &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	} else {
		admissionResp = mutate(admissionReview.Request)
	}

	admissionReviewResp := admissionv1.AdmissionReview{
		TypeMeta: admissionReview.TypeMeta,
		Response: admissionResp,
	}

	respBytes, err := json.Marshal(admissionReviewResp)
	if err != nil {
		klog.Error("encode response failed: ", err)
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(respBytes)
}

func mutate(admissionReq *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// Unmarshal the Pod object
	var err error
	pod := &corev1.Pod{}
	if err = json.Unmarshal(admissionReq.Object.Raw, pod); err != nil {
		klog.Error("unmarshal pod failed: ", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	var ownerName string
	var workloadType string

	ownerName, workloadType, err = queryOwnerOfPod(pod)
	if err != nil {
		klog.Error("query owner of pod failed: ", err)
		return &admissionv1.AdmissionResponse{
			UID: admissionReq.UID,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	if workloadType == "standalone" {
		return &admissionv1.AdmissionResponse{
			UID:     admissionReq.UID,
			Allowed: true,
		}
	}

	patches, err := injectAffinityRules(pod, ownerName, workloadType)
	if err != nil {
		klog.Error("generate patch failed: ", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	patch, err := json.Marshal(patches)
	if err != nil {
		klog.Error("marshal patch failed: ", err)
		return &admissionv1.AdmissionResponse{
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}
	}

	return &admissionv1.AdmissionResponse{
		UID:       admissionReq.UID,
		Allowed:   true,
		Patch:     []byte(patch),
		PatchType: ptr.To(admissionv1.PatchTypeJSONPatch),
	}

}

// queryOwnerOfPod queries the owner of the pod and returns the owner name and workload type
func queryOwnerOfPod(pod *corev1.Pod) (ownerName string, workloadType string, err error) {

	workloadType = workloadTypeStandalone

	for _, ownerRef := range pod.OwnerReferences {
		if ownerRef.Kind == "ReplicaSet" {
			// Find the owner of the ReplicaSet
			rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), ownerRef.Name, metav1.GetOptions{})
			if err != nil {
				return "", "", err
			}
			for i := range rs.OwnerReferences {
				if rs.OwnerReferences[i].Kind == workloadTypeDeployment {
					workloadType = workloadTypeDeployment
					ownerName = rs.OwnerReferences[i].Name
				}
			}
		} else if ownerRef.Kind == workloadTypeStatefulSet {
			workloadType = workloadTypeStatefulSet
			ownerName = ownerRef.Name
		}
	}
	return ownerName, workloadType, nil
}

// injectAffinityRules injects the affinity rules into the Pod that is created by Deployment or StatefulSet
func injectAffinityRules(pod *corev1.Pod, ownerName string, workloadType string) (patches []PatchOperation, err error) {
	patches = []PatchOperation{}
	addNodeAffinityPatch := func(nodeAffinity *corev1.NodeAffinity) {
		if nodeAffinity == nil {
			return
		}
		var mergedAffinity *corev1.Affinity

		// Set nodeAffinity directly if the original node affinity is empty.
		// Otherwise merge the nodeAffinity.
		if pod.Spec.Affinity == nil {
			mergedAffinity = &corev1.Affinity{
				NodeAffinity: nodeAffinity,
			}
			patches = append(patches, PatchOperation{
				Op:    "add",
				Path:  "/spec/affinity",
				Value: mergedAffinity,
			})
			return
		}
		if pod.Spec.Affinity.NodeAffinity == nil {
			mergedAffinity = pod.Spec.Affinity.DeepCopy()
			mergedAffinity.NodeAffinity = nodeAffinity
		} else {
			mergedAffinity = pod.Spec.Affinity.DeepCopy()
			if nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil {
				if mergedAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
					mergedAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution = nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				} else {
					mergedAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms = append(mergedAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms, nodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms...)
				}
			}
			if nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution != nil {
				if mergedAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution == nil {
					mergedAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution
				} else {
					mergedAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution = append(mergedAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution, nodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution...)
				}
			}
		}

		patches = append(patches, PatchOperation{
			Op:    "replace",
			Path:  "/spec/affinity",
			Value: mergedAffinity,
		})

	}
	scheduleToOnDemand := func() {
		nodeAffinity := &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "node.kubernetes.io/capacity",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"on-demand"},
							},
						},
					},
				},
			},
		}
		addNodeAffinityPatch(nodeAffinity)
		if pod.Annotations == nil {
			annotations :=map[string]string{
				"controller.kubernetes.io/pod-deletion-cost": "1",
			}
			patches = append(patches, PatchOperation{
				Op:    "add",
				Path:  "/metadata/annotations",
				Value: annotations,
			})
		} else {
			annotations := maps.Clone(pod.Annotations)
			annotations["controller.kubernetes.io/pod-deletion-cost"] = "1"
			patches = append(patches, PatchOperation{
				Op:    "replace",
				Path:  "/metadata/annotations",
				Value: annotations,
			})
		}
		patches = append(patches, PatchOperation{
			Op:    "add",
			Path:  "/metadata/labels/distributed-scheduling~1desired-capacity",
			Value: "on-demand",
		})
	}
	scheduleToSpot := func() {
		nodeAffinity := &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 100,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "node.kubernetes.io/capacity",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"spot"},
							},
						},
					},
				},
			},
		}
		addNodeAffinityPatch(nodeAffinity)
		patches = append(patches, PatchOperation{
			Op:    "add",
			Path:  "/metadata/labels/distributed-scheduling~1desired-capacity",
			Value: "spot",
		})
	}
	if workloadType == workloadTypeDeployment {
		deployment, err := clientset.AppsV1().Deployments(pod.Namespace).Get(context.TODO(), ownerName, metav1.GetOptions{})
		if err != nil {
			return patches, err
		}
		selector, _ := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		requirement, _ := labels.NewRequirement("distributed-scheduling/desired-capacity", selection.Equals, []string{"on-demand"})
		selector.Add(*requirement)
		pods, err := clientset.CoreV1().Pods(pod.Namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
		if err != nil {
			return patches, err
		}

		minAvailable, err := strconv.Atoi(deployment.Annotations["distributed-scheduling/min-available"])
		if err != nil {
			return patches, fmt.Errorf("value of distributed-scheduling/min-available is not a number: %s", err.Error())
		}

		if len(pods.Items) < minAvailable {
			scheduleToOnDemand()
		} else {
			scheduleToSpot()
		}
	}

	if workloadType == workloadTypeStatefulSet {
		statefulset, err := clientset.AppsV1().StatefulSets(pod.Namespace).Get(context.TODO(), ownerName, metav1.GetOptions{})
		if err != nil {
			return patches, err
		}
		minAvailable, err := strconv.Atoi(statefulset.Annotations["distributed-scheduling/min-available"])
		if err != nil {
			return patches, fmt.Errorf("value of distributed-scheduling/min-available is not a number: %s", err.Error())
		}
		podIndex, _ := strconv.Atoi(pod.Name[len(ownerName)+1:])
		if podIndex < minAvailable {
			scheduleToOnDemand()
		} else {
			scheduleToSpot()
		}
	}

	return patches, nil
}

func getClient() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}
