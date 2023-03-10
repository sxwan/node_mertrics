package capacity

import (
	"context"
	"encoding/json"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	resourcehelper "k8s.io/kubectl/pkg/util/resource"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
)

const Mebibyte = 1024 * 1024

type resourceMetric struct {
	resourceType string
	allocatable  resource.Quantity
	request      resource.Quantity
	limit        resource.Quantity
	labels       map[string]string
}

type clusterMetric struct {
	cpu         *resourceMetric
	memory      *resourceMetric
	gpu         *resourceMetric
	labels      *resourceMetric
	nodeMetrics map[string]*nodeMetric
}

type nodeMetric struct {
	name       string
	cpu        *resourceMetric
	memory     *resourceMetric
	gpu        *resourceMetric
	labels     *resourceMetric
	podMetrics map[string]*podMetric
}

type podMetric struct {
	name             string
	namespace        string
	cpu              *resourceMetric
	memory           *resourceMetric
	gpu              *resourceMetric
	labels           *resourceMetric
	containerMetrics map[string]*containerMetric
}

type containerMetric struct {
	name   string
	cpu    *resourceMetric
	memory *resourceMetric
	gpu    *resourceMetric
}
type tablePrinter struct {
	cm              *clusterMetric
	w               *tabwriter.Writer
	availableFormat bool
}

type tableLine struct {
	node          string
	cpuRequest    string
	cpuLimits     string
	memoryRequest string
	memoryLimits  string
	gpuRequest    string
	gpuLimits     string
	labels        string
}

var headerStrings = tableLine{
	node:          "NODE",
	cpuRequest:    "CPU REQUEST",
	cpuLimits:     "CPU LIMITS",
	memoryRequest: "MEMORY REQUESTS",
	memoryLimits:  "MEMORY LIMITS",
	gpuRequest:    "GPU REQUEST",
	gpuLimits:     "GPU LIMITES",
	labels:        "LABELS",
}

func (tp *tablePrinter) Print(availableFormat bool) {
	tp.w.Init(os.Stdout, 0, 8, 2, ' ', 0)
	tp.availableFormat = availableFormat
	NodeMetrics := tp.cm.getNodeMetrics()

	tp.PrintLine(&headerStrings)

	if len(NodeMetrics) > 1 {
		tp.PrintClusterLine()
	}
	for _, nm := range NodeMetrics {
		tp.PrintNodeLine(nm.name, nm)
	}
	err := tp.w.Flush()
	if err != nil {
		fmt.Printf("Error writing to table: %s", err)
	}
}

func (tp *tablePrinter) PrintLine(tl *tableLine) {
	lineItems := tp.PrintLineItems(tl)
	fmt.Fprintf(tp.w, strings.Join(lineItems[:], "\t")+"\n")
}

func (tp *tablePrinter) PrintLineItems(tl *tableLine) []string {
	lineItems := []string{tl.node}
	lineItems = append(lineItems, tl.cpuRequest)
	lineItems = append(lineItems, tl.cpuLimits)
	lineItems = append(lineItems, tl.memoryRequest)
	lineItems = append(lineItems, tl.memoryLimits)
	lineItems = append(lineItems, tl.gpuRequest)
	lineItems = append(lineItems, tl.gpuLimits)
	lineItems = append(lineItems, tl.labels)
	return lineItems
}

func (tp *tablePrinter) PrintClusterLine() {
	tp.PrintLine(&tableLine{
		node:          "*",
		cpuRequest:    tp.cm.cpu.requestString(tp.availableFormat),
		cpuLimits:     tp.cm.cpu.limitString(tp.availableFormat),
		memoryRequest: tp.cm.memory.requestString(tp.availableFormat),
		memoryLimits:  tp.cm.memory.limitString(tp.availableFormat),
		gpuRequest:    tp.cm.gpu.requestString(tp.availableFormat),
		gpuLimits:     tp.cm.gpu.limitString(tp.availableFormat),
		labels:        tp.cm.labels.labelsString(tp.availableFormat),
	})
}
func (tp *tablePrinter) PrintNodeLine(nodeName string, nm *nodeMetric) {
	tp.PrintLine(&tableLine{
		node:          nodeName,
		cpuRequest:    nm.cpu.requestString(tp.availableFormat),
		cpuLimits:     nm.cpu.limitString(tp.availableFormat),
		memoryRequest: nm.memory.requestString(tp.availableFormat),
		memoryLimits:  nm.memory.limitString(tp.availableFormat),
		gpuRequest:    nm.gpu.requestString(tp.availableFormat),
		gpuLimits:     nm.gpu.limitString(tp.availableFormat),
		labels:        nm.labels.labelsString(tp.availableFormat),
	})
}

// ???node????????????
func (cm *clusterMetric) getNodeMetrics() []*nodeMetric {
	NodeMetrics := make([]*nodeMetric, len(cm.nodeMetrics))

	i := 0
	for name := range cm.nodeMetrics {
		NodeMetrics[i] = cm.nodeMetrics[name]
		i++
	}

	sort.Slice(NodeMetrics, func(i, j int) bool {
		return NodeMetrics[i].name < NodeMetrics[j].name
	})

	return NodeMetrics
}

func getPodsAndNodes(client kubernetes.Interface) (*corev1.PodList, *corev1.NodeList) {
	nodeList, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error listing Nodes: %v\n", err)
		os.Exit(2)
	}
	podList, err := client.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error listing Pods: %v\n", err)
		os.Exit(3)
	}
	newPodItems := []corev1.Pod{}
	nodes := map[string]bool{}

	for _, node := range nodeList.Items {
		nodes[node.GetName()] = true
	}
	for _, pod := range podList.Items {
		if !nodes[pod.Spec.NodeName] {
			continue
		}

		newPodItems = append(newPodItems, pod)
	}
	podList.Items = newPodItems
	return podList, nodeList
}

func buildClusterMetric(podList *corev1.PodList, pmList *v1beta1.PodMetricsList, nodeList *corev1.NodeList, nmList *v1beta1.NodeMetricsList) clusterMetric {
	cm := clusterMetric{
		cpu:         &resourceMetric{resourceType: "cpu"},
		memory:      &resourceMetric{resourceType: "memory"},
		gpu:         &resourceMetric{resourceType: "gpu"},
		labels:      &resourceMetric{resourceType: "labels"},
		nodeMetrics: map[string]*nodeMetric{},
	}
	var totalPodAllocatable int64
	var totalPodCurrent int64
	for _, node := range nodeList.Items {
		var tmpPodCount int64
		for _, pod := range podList.Items {
			if pod.Spec.NodeName == node.Name && pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
				tmpPodCount++
			}
		}
		totalPodCurrent += tmpPodCount
		totalPodAllocatable += node.Status.Allocatable.Pods().Value()
		cm.nodeMetrics[node.Name] = &nodeMetric{
			name: node.Name,
			cpu: &resourceMetric{
				resourceType: "cpu",
				allocatable:  node.Status.Allocatable["cpu"],
			},
			memory: &resourceMetric{
				resourceType: "memory",
				allocatable:  node.Status.Allocatable["memory"],
			},
			gpu: &resourceMetric{
				resourceType: "gpu",
				allocatable:  node.Status.Allocatable["gpu"],
			},
			labels: &resourceMetric{
				resourceType: "labels",
				labels:       node.ObjectMeta.Labels,
			},
			podMetrics: map[string]*podMetric{},
		}
	}

	podMetrics := map[string]v1beta1.PodMetrics{}
	if pmList != nil {
		for _, pm := range pmList.Items {
			podMetrics[fmt.Sprintf("%s-%s", pm.GetNamespace(), pm.GetName())] = pm
		}
	}

	for _, pod := range podList.Items {
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			cm.addPodMetric(&pod, podMetrics[fmt.Sprintf("%s-%s", pod.GetNamespace(), pod.GetName())])
		}
	}

	for _, node := range nodeList.Items {
		if nm, ok := cm.nodeMetrics[node.Name]; ok {
			cm.addNodeMetric(nm)
		}
	}

	return cm
}

func (rm *resourceMetric) addMetric(m *resourceMetric) {
	rm.allocatable.Add(m.allocatable)
	rm.request.Add(m.request)
	rm.limit.Add(m.limit)
}

func (cm *clusterMetric) addPodMetric(pod *corev1.Pod, podMetrics v1beta1.PodMetrics) {
	req, limit := resourcehelper.PodRequestsAndLimits(pod)
	key := fmt.Sprintf("%s-%s", pod.Namespace, pod.Name)
	nm := cm.nodeMetrics[pod.Spec.NodeName]

	pm := &podMetric{
		name:      pod.Name,
		namespace: pod.Namespace,
		cpu: &resourceMetric{
			resourceType: "cpu",
			request:      req["cpu"],
			limit:        limit["cpu"],
		},
		memory: &resourceMetric{
			resourceType: "memory",
			request:      req["memory"],
			limit:        limit["memory"],
		},
		gpu: &resourceMetric{
			resourceType: "gpu",
			request:      req["gpu"],
			limit:        limit["gpu"],
		},
		labels: &resourceMetric{
			resourceType: "labels",
			labels:       pod.ObjectMeta.Labels,
		},
		containerMetrics: map[string]*containerMetric{},
	}

	for _, container := range pod.Spec.Containers {
		pm.containerMetrics[container.Name] = &containerMetric{
			name: container.Name,
			cpu: &resourceMetric{
				resourceType: "cpu",
				request:      container.Resources.Requests["cpu"],
				limit:        container.Resources.Limits["cpu"],
				allocatable:  nm.cpu.allocatable,
			},
			memory: &resourceMetric{
				resourceType: "memory",
				request:      container.Resources.Requests["memory"],
				limit:        container.Resources.Limits["memory"],
				allocatable:  nm.memory.allocatable,
			},
			gpu: &resourceMetric{
				resourceType: "gpu",
				request:      container.Resources.Requests["gpu"],
				limit:        container.Resources.Limits["gpu"],
				allocatable:  nm.gpu.allocatable,
			},
		}

		if nm != nil {
			nm.podMetrics[key] = pm
			nm.podMetrics[key].cpu.allocatable = nm.cpu.allocatable
			nm.podMetrics[key].memory.allocatable = nm.memory.allocatable

			nm.cpu.request.Add(req["cpu"])
			nm.cpu.limit.Add(limit["cpu"])
			nm.memory.request.Add(req["memory"])
			nm.memory.limit.Add(limit["memory"])
			nm.gpu.request.Add(req["gpu"])
			nm.gpu.limit.Add(limit["gpu"])
		}
	}
}

func (cm *clusterMetric) addNodeMetric(nm *nodeMetric) {
	cm.cpu.addMetric(nm.cpu)
	cm.memory.addMetric(nm.memory)
	cm.gpu.addMetric(nm.gpu)
}

func resourceString(resourceType string, actual, allocatable resource.Quantity, avaliableFormat bool) string {
	utilPercent := float64(0)
	if allocatable.MilliValue() > 0 {
		utilPercent = float64(actual.MilliValue()) / float64(allocatable.MilliValue())
	}

	var actualStr, allocatableStr string

	if avaliableFormat {
		switch resourceType {
		case "cpu":
			actualStr = fmt.Sprintf("%dm", allocatable.MilliValue()-actual.MilliValue())
			allocatableStr = fmt.Sprintf("%dm", allocatable.MilliValue())
		case "memory":
			actualStr = fmt.Sprintf("%dMi", formatToMegiBytes(allocatable)-formatToMegiBytes(actual))
			allocatableStr = fmt.Sprintf("%dMi", formatToMegiBytes(allocatable))
		case "gpu":
			actualStr = fmt.Sprintf("%d", formatToMegiBytes(allocatable)-formatToMegiBytes(actual))
			allocatableStr = fmt.Sprintf("%d", formatToMegiBytes(allocatable))
		default:
			actualStr = fmt.Sprintf("%d", allocatable.Value()-actual.Value())
			allocatableStr = fmt.Sprintf("%d", allocatable.Value())
		}
		return fmt.Sprintf("%s/%s", actualStr, allocatableStr)
	}
	switch resourceType {
	case "cpu":
		actualStr = fmt.Sprintf("%dm", actual.MilliValue())
	case "memory":
		actualStr = fmt.Sprintf("%dMi", formatToMegiBytes(actual))
	case "gpu":
		actualStr = fmt.Sprintf("%d", actual.Value())
	default:
		actualStr = fmt.Sprintf("%d", actual.Value())
	}

	return fmt.Sprintf("%s (%d%%%%)", actualStr, int64(utilPercent))
}

func formatToMegiBytes(actual resource.Quantity) int64 {
	value := actual.Value() / Mebibyte
	if actual.Value()%Mebibyte != 0 {
		value++
	}
	return value
}

func (rm *resourceMetric) requestString(availableFormat bool) string {
	return resourceString(rm.resourceType, rm.request, rm.allocatable, availableFormat)
}

func (rm *resourceMetric) limitString(availableFormat bool) string {
	return resourceString(rm.resourceType, rm.limit, rm.allocatable, availableFormat)
}

func (rm *resourceMetric) labelsString(availableFormat bool) string {
	jsonBytes, err := json.Marshal(rm.labels)
	if err != nil {
		panic(err)
	}

	jsonString := string(jsonBytes)
	return jsonString
}
