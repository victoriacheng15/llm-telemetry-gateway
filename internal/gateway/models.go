package gateway

import (
	"sync"
	"time"
)

type MetricEntry struct {
	Timestamp time.Time
	Duration  float64 // in seconds
	Tokens    int64
}

type SystemMetric struct {
	Timestamp time.Time
	CPU       float64 // percent relative to limits
	Memory    int64   // bytes
}

type MetricsTracker struct {
	mu            sync.RWMutex
	requests      []MetricEntry
	systemMetrics []SystemMetric

	lastCPUUsage int64
	lastCPUTime  time.Time
}

type MetricsResponse struct {
	CPUUsage          float64   `json:"cpu_usage"`
	CPUHistory        []float64 `json:"cpu_history"`
	MemoryUsage       int64     `json:"memory_usage"`
	MemoryHistory     []int64   `json:"memory_history"`
	TokenThroughput   float64   `json:"token_throughput"`
	TokenHistory      []float64 `json:"token_history"`
	LatencyAvg        float64   `json:"latency_avg"`
	LatencyAvgHistory []float64 `json:"latency_avg_history"`
	LatencyP95        float64   `json:"latency_p95"`
	LatencyP95History []float64 `json:"latency_p95_history"`
	LatencyP99        float64   `json:"latency_p99"`
	LatencyP99History []float64 `json:"latency_p99_history"`
}

type MaskRequest struct {
	Prompt string `json:"prompt"`
}

type MaskResponse struct {
	Masked string `json:"masked"`
}

type ChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}
