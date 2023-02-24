package capacity

import (
	"fmt"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	"node_metrics/kube"
	"os"
	"text/tabwriter"
)

var availableFormat bool

func printList(cm *clusterMetric) {
	tp := &tablePrinter{
		cm:              cm,
		w:               new(tabwriter.Writer),
		availableFormat: availableFormat,
	}
	tp.Print(true)
}

func FetchAndPrint() {
	clientset, err := kube.NewClient()
	if err != nil {
		fmt.Printf("Error connecting to Kubernetes: %v\n", err)
		os.Exit(1)
	}
	podList, nodeList := getPodsAndNodes(clientset)
	var pmList *v1beta1.PodMetricsList
	var nmList *v1beta1.NodeMetricsList
	cm := buildClusterMetric(podList, pmList, nodeList, nmList)

	printList(&cm)
}
