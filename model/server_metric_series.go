package model

// ServerMetricSeries is a compact columnar payload for node history charts.
type ServerMetricSeries struct {
	Range    string    `json:"range"`
	StepSec  int64     `json:"stepSec"`
	From     int64     `json:"from,omitempty"`
	To       int64     `json:"to,omitempty"`
	Count    int       `json:"count"`
	T        []int64   `json:"t"`
	CPU      []float64 `json:"cpu"`
	Mem      []float64 `json:"mem"`
	MemUsed  []uint64  `json:"memUsed"`
	Disk     []float64 `json:"disk"`
	DiskUsed []uint64  `json:"diskUsed"`
	NetIn    []float64 `json:"netIn"`
	NetOut   []float64 `json:"netOut"`
}
