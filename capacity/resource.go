package capacity


const Mebibyte = 1024 * 1024

type resourceMetric struct {
	resourceType string
	allocatable  resource.Quantity
	utilization  resource.Quantity
	request      resource.Quantity
	limit        resource.Quantity
}

type clusterMertric struct{
	cpu *resourceMetric
	memory *resourceMetric
}