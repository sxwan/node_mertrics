package capacity

type tablePrinter struct {
	cm              *clusterMetric
	w               *tabwriter.Writer
	availableFormat bool
}