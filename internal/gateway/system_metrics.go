package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	cpuStatPath      = "/sys/fs/cgroup/cpu.stat"
	cpuacctUsagePath = "/sys/fs/cgroup/cpuacct/cpuacct.usage"
	procStatPath     = "/proc/self/stat"
	memCurrentPath   = "/sys/fs/cgroup/memory.current"
	memUsagePath     = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
	procStatusPath   = "/proc/self/status"
)

func readCPUUsageCgroupV2() (int64, error) {
	data, err := os.ReadFile(cpuStatPath)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "usage_usec" {
			val, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return val * 1000, nil // return nanoseconds
			}
		}
	}
	return 0, fmt.Errorf("usage_usec not found")
}

func readCPUUsageCgroupV1() (int64, error) {
	data, err := os.ReadFile(cpuacctUsagePath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

func readCPUUsageProc() int64 {
	data, err := os.ReadFile(procStatPath)
	if err != nil {
		return 0
	}
	idx := strings.LastIndex(string(data), ")")
	if idx == -1 {
		return 0
	}
	fields := strings.Fields(string(data[idx+1:]))
	if len(fields) >= 13 {
		utime, err1 := strconv.ParseInt(fields[11], 10, 64)
		stime, err2 := strconv.ParseInt(fields[12], 10, 64)
		if err1 == nil && err2 == nil {
			return (utime + stime) * 10000000 // Convert to ns (10ms per tick at 100 HZ)
		}
	}
	return 0
}

func readMemoryUsageCgroupV2() (int64, error) {
	data, err := os.ReadFile(memCurrentPath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

func readMemoryUsageCgroupV1() (int64, error) {
	data, err := os.ReadFile(memUsagePath)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
}

func readMemoryUsageProc() (int64, error) {
	data, err := os.ReadFile(procStatusPath)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				val, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return val * 1024, nil // return bytes
				}
			}
		}
	}
	return 0, fmt.Errorf("VmRSS not found")
}

func readMemoryUsageGo() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Alloc)
}

func parseCPULimit(limitStr string) float64 {
	if limitStr == "" {
		return float64(runtime.NumCPU())
	}
	if strings.HasSuffix(limitStr, "m") {
		milli, err := strconv.ParseFloat(strings.TrimSuffix(limitStr, "m"), 64)
		if err == nil {
			return milli / 1000.0
		}
	}
	val, err := strconv.ParseFloat(limitStr, 64)
	if err == nil {
		return val
	}
	return float64(runtime.NumCPU())
}

func parseMemoryLimit(limitStr string) int64 {
	if limitStr == "" {
		return 512 * 1024 * 1024 // 512Mi default
	}
	limitStr = strings.TrimSpace(limitStr)
	multiplier := int64(1)
	unit := ""
	for i := len(limitStr) - 1; i >= 0; i-- {
		ch := limitStr[i]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			unit = limitStr[i+1:]
			limitStr = limitStr[:i+1]
			break
		}
	}
	unit = strings.ToUpper(unit)
	if strings.HasPrefix(unit, "G") {
		multiplier = 1024 * 1024 * 1024
	} else if strings.HasPrefix(unit, "M") {
		multiplier = 1024 * 1024
	} else if strings.HasPrefix(unit, "K") {
		multiplier = 1024
	}
	val, err := strconv.ParseFloat(limitStr, 64)
	if err == nil {
		return int64(val * float64(multiplier))
	}
	return 512 * 1024 * 1024
}

func (t *MetricsTracker) RecordRequest(duration float64, tokens int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.requests = append(t.requests, MetricEntry{
		Timestamp: now,
		Duration:  duration,
		Tokens:    tokens,
	})

	// Prune older than 3 hours
	cutoff := now.Add(-3 * time.Hour)
	idx := 0
	for i, req := range t.requests {
		if req.Timestamp.After(cutoff) {
			idx = i
			break
		}
	}
	if idx > 0 {
		t.requests = t.requests[idx:]
	}
}

func (t *MetricsTracker) StartCollector(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.collectSystemMetrics()
		}
	}
}

func (t *MetricsTracker) collectSystemMetrics() {
	now := time.Now()

	// Read CPU usage
	var cpuNS int64
	var err error
	cpuNS, err = readCPUUsageCgroupV2()
	if err != nil {
		cpuNS, err = readCPUUsageCgroupV1()
	}
	if err != nil {
		cpuNS = readCPUUsageProc()
	}

	// Read Memory usage
	var memBytes int64
	memBytes, err = readMemoryUsageCgroupV2()
	if err != nil {
		memBytes, err = readMemoryUsageCgroupV1()
	}
	if err != nil {
		memBytes, err = readMemoryUsageProc()
	}
	if err != nil {
		memBytes = readMemoryUsageGo()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var cpuPct float64
	if !t.lastCPUTime.IsZero() {
		deltaNS := cpuNS - t.lastCPUUsage
		deltaTime := now.Sub(t.lastCPUTime).Nanoseconds()
		if deltaTime > 0 && deltaNS >= 0 {
			cpuLimit := parseCPULimit(os.Getenv("LIMITS_CPU"))
			cpuPct = (float64(deltaNS) / float64(deltaTime)) * 100.0 / cpuLimit
			if cpuPct > 100.0 {
				cpuPct = 100.0
			}
		}
	}
	t.lastCPUUsage = cpuNS
	t.lastCPUTime = now

	// Append to systemMetrics
	t.systemMetrics = append(t.systemMetrics, SystemMetric{
		Timestamp: now,
		CPU:       cpuPct,
		Memory:    memBytes,
	})

	// Prune older than 3 hours
	cutoff := now.Add(-3 * time.Hour)
	idx := 0
	for i, sm := range t.systemMetrics {
		if sm.Timestamp.After(cutoff) {
			idx = i
			break
		}
	}
	if idx > 0 {
		t.systemMetrics = t.systemMetrics[idx:]
	}
}

func (t *MetricsTracker) GetMetrics(duration time.Duration) MetricsResponse {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := time.Now()
	startTime := now.Add(-duration)

	var currentCPU float64
	var currentMem int64
	if len(t.systemMetrics) > 0 {
		latest := t.systemMetrics[len(t.systemMetrics)-1]
		currentCPU = latest.CPU
		currentMem = latest.Memory
	}

	var currentTokens float64
	recentStart := now.Add(-10 * time.Second)
	var recentTokens int64
	for i := len(t.requests) - 1; i >= 0; i-- {
		req := t.requests[i]
		if req.Timestamp.Before(recentStart) {
			break
		}
		recentTokens += req.Tokens
	}
	currentTokens = float64(recentTokens) / 10.0

	var currentAvg, currentP95, currentP99 float64
	var recentDurations []float64
	for i := len(t.requests) - 1; i >= 0; i-- {
		req := t.requests[i]
		if req.Timestamp.Before(recentStart) {
			break
		}
		recentDurations = append(recentDurations, req.Duration*1000) // convert to ms
	}
	if len(recentDurations) > 0 {
		sort.Float64s(recentDurations)
		var sum float64
		for _, d := range recentDurations {
			sum += d
		}
		currentAvg = sum / float64(len(recentDurations))
		currentP95 = recentDurations[int(float64(len(recentDurations))*0.95)]
		currentP99 = recentDurations[int(float64(len(recentDurations))*0.99)]
	} else if len(t.requests) > 0 {
		var sum float64
		allDurs := make([]float64, len(t.requests))
		for idx, req := range t.requests {
			val := req.Duration * 1000
			sum += val
			allDurs[idx] = val
		}
		sort.Float64s(allDurs)
		currentAvg = sum / float64(len(t.requests))
		currentP95 = allDurs[int(float64(len(allDurs))*0.95)]
		currentP99 = allDurs[int(float64(len(allDurs))*0.99)]
	}

	numPoints := 30
	step := duration / time.Duration(numPoints)

	cpuHistory := make([]float64, numPoints)
	memHistory := make([]int64, numPoints)
	tokenHistory := make([]float64, numPoints)
	latencyAvgHistory := make([]float64, numPoints)
	latencyP95History := make([]float64, numPoints)
	latencyP99History := make([]float64, numPoints)

	for i := 0; i < numPoints; i++ {
		intStart := startTime.Add(step * time.Duration(i))
		intEnd := intStart.Add(step)

		var cpuSum float64
		var memSum int64
		var sysCount int
		for _, sm := range t.systemMetrics {
			if (sm.Timestamp.After(intStart) || sm.Timestamp.Equal(intStart)) && sm.Timestamp.Before(intEnd) {
				cpuSum += sm.CPU
				memSum += sm.Memory
				sysCount++
			}
		}

		if sysCount > 0 {
			cpuHistory[i] = cpuSum / float64(sysCount)
			memHistory[i] = memSum / int64(sysCount)
		} else {
			if i > 0 {
				cpuHistory[i] = cpuHistory[i-1]
				memHistory[i] = memHistory[i-1]
			} else {
				cpuHistory[i] = currentCPU
				memHistory[i] = currentMem
			}
		}

		var intervalTokens int64
		var intervalDurations []float64
		for _, req := range t.requests {
			if (req.Timestamp.After(intStart) || req.Timestamp.Equal(intStart)) && req.Timestamp.Before(intEnd) {
				intervalTokens += req.Tokens
				intervalDurations = append(intervalDurations, req.Duration*1000) // in ms
			}
		}

		tokenHistory[i] = float64(intervalTokens) / step.Seconds()

		if len(intervalDurations) > 0 {
			sort.Float64s(intervalDurations)
			var sum float64
			for _, d := range intervalDurations {
				sum += d
			}
			latencyAvgHistory[i] = sum / float64(len(intervalDurations))
			latencyP95History[i] = intervalDurations[int(float64(len(intervalDurations))*0.95)]
			latencyP99History[i] = intervalDurations[int(float64(len(intervalDurations))*0.99)]
		} else {
			if i > 0 {
				latencyAvgHistory[i] = latencyAvgHistory[i-1]
				latencyP95History[i] = latencyP95History[i-1]
				latencyP99History[i] = latencyP99History[i-1]
			}
		}
	}

	return MetricsResponse{
		CPUUsage:          currentCPU,
		CPUHistory:        cpuHistory,
		MemoryUsage:       currentMem,
		MemoryHistory:     memHistory,
		TokenThroughput:   currentTokens,
		TokenHistory:      tokenHistory,
		LatencyAvg:        currentAvg,
		LatencyAvgHistory: latencyAvgHistory,
		LatencyP95:        currentP95,
		LatencyP95History: latencyP95History,
		LatencyP99:        currentP99,
		LatencyP99History: latencyP99History,
	}
}

func (t *MetricsTracker) getProxyStats() (int, int64, float64) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	requestCount := len(t.requests)
	var totalTokens int64
	var totalDuration float64
	for _, req := range t.requests {
		totalTokens += req.Tokens
		totalDuration += req.Duration
	}
	avgDuration := float64(0)
	if requestCount > 0 {
		avgDuration = totalDuration / float64(requestCount)
	}
	return requestCount, totalTokens, avgDuration
}

func buildPromptContext(cpuUsage float64, memUsage int64, memLimitBytes int64, reqCount int, totalTokens int64, avgDuration float64, anomalies []string) string {
	nowStr := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	status := "HEALTHY"
	if len(anomalies) > 0 {
		status = "ANOMALOUS"
	}
	memUsagePct := float64(0)
	if memLimitBytes > 0 {
		memUsagePct = (float64(memUsage) / float64(memLimitBytes)) * 100.0
	}

	var lines []string
	lines = append(lines, "=== Telemetry Diagnostics Context ===")
	lines = append(lines, fmt.Sprintf("Timestamp: %s", nowStr))
	lines = append(lines, fmt.Sprintf("System Status: %s", status))
	lines = append(lines, "")
	lines = append(lines, "Metrics Snapshot:")
	lines = append(lines, fmt.Sprintf("- Pod CPU Utilization: %.1f%%", cpuUsage))
	lines = append(lines, fmt.Sprintf("- Pod Memory Utilization: %.1f%%", memUsagePct))
	lines = append(lines, fmt.Sprintf("- Total Requests Processed: %d", reqCount))
	lines = append(lines, fmt.Sprintf("- Total Tokens: %d", totalTokens))
	lines = append(lines, fmt.Sprintf("- Average Response Duration: %.1fms", avgDuration*1000.0))

	if len(anomalies) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Detected Anomalies:")
		for _, anomaly := range anomalies {
			lines = append(lines, fmt.Sprintf("[ALERT] %s", anomaly))
		}
	}
	lines = append(lines, "=====================================")
	return strings.Join(lines, "\n")
}

func (t *MetricsTracker) StartTelemetryEvaluator(ctx context.Context) {
	// Wait a bit on startup for metrics servers to initialize
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	slog.Info("Telemetry evaluation loop started.")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.evaluateTelemetry(ctx)
		}
	}
}

func (t *MetricsTracker) evaluateTelemetry(ctx context.Context) {
	var cpuUsage float64
	var memUsage int64
	t.mu.RLock()
	if len(t.systemMetrics) > 0 {
		latest := t.systemMetrics[len(t.systemMetrics)-1]
		cpuUsage = latest.CPU
		memUsage = latest.Memory
	}
	t.mu.RUnlock()

	var anomalies []string
	cpuThreshold := 80.0
	if envVal := os.Getenv("ANOMALY_CPU_THRESHOLD"); envVal != "" {
		if val, err := strconv.ParseFloat(envVal, 64); err == nil {
			cpuThreshold = val
		}
	}
	if cpuUsage > cpuThreshold {
		anomalies = append(anomalies, fmt.Sprintf("High Pod CPU Utilization: %.2f%%", cpuUsage))
	}

	memLimitBytes := parseMemoryLimit(os.Getenv("LIMITS_MEMORY"))
	memUsagePct := float64(0)
	if memLimitBytes > 0 {
		memUsagePct = (float64(memUsage) / float64(memLimitBytes)) * 100.0
	}
	memThreshold := 80.0
	if envVal := os.Getenv("ANOMALY_MEMORY_THRESHOLD"); envVal != "" {
		if val, err := strconv.ParseFloat(envVal, 64); err == nil {
			memThreshold = val
		}
	}
	if memUsagePct > memThreshold {
		anomalies = append(anomalies, fmt.Sprintf("High Pod Memory Utilization: %.2f%%", memUsagePct))
	}

	// Check for UDS connectability (PII policy engine health)
	udsConn, err := net.DialTimeout("unix", SocketPath, 1*time.Second)
	if err != nil {
		anomalies = append(anomalies, "Go Proxy readyz check failed (PII policy engine unreachable)")
	} else {
		udsConn.Close()
	}

	// Check recent latencies
	t.mu.RLock()
	hasHighLatency := false
	var lastDur float64
	latencyThreshold := 0.2
	if envVal := os.Getenv("ANOMALY_LATENCY_THRESHOLD"); envVal != "" {
		if val, err := strconv.ParseFloat(envVal, 64); err == nil {
			latencyThreshold = val
		}
	}
	for i := len(t.requests) - 1; i >= 0; i-- {
		if time.Since(t.requests[i].Timestamp) > 10*time.Second {
			break
		}
		if t.requests[i].Duration > latencyThreshold {
			hasHighLatency = true
			lastDur = t.requests[i].Duration
			break
		}
	}
	t.mu.RUnlock()

	if hasHighLatency {
		anomalies = append(anomalies, fmt.Sprintf("High request latency observed: %.1fms", lastDur*1000.0))
	}

	reqCount, totalTokens, avgDuration := t.getProxyStats()

	latestContext := buildPromptContext(cpuUsage, memUsage, memLimitBytes, reqCount, totalTokens, avgDuration, anomalies)

	// Write diagnostics to file
	err = os.WriteFile(DiagnosticsPath, []byte(latestContext), 0644)
	if err != nil {
		slog.Error("Failed to write diagnostics file", "error", err)
	}

	// RCA Diagnosis using Ollama via Go completions endpoint
	var rcaText string
	if len(anomalies) > 0 {
		prompt := fmt.Sprintf(
			"You are an AIOps diagnostic agent. Analyze the system telemetry context below and perform a "+
				"Root Cause Analysis (RCA) to identify which active chaos scenario is happening.\n"+
				"Choose exactly one of the following scenarios:\n"+
				"1. \"Network Delay\" (high request duration/latency)\n"+
				"2. \"Sidecar Process Crash\" (healthz/readyz check failing, connection refused)\n"+
				"3. \"Resource Starvation / Stress\" (high pod CPU or memory utilization)\n"+
				"4. \"Healthy / Nominal\" (no anomalies)\n\n"+
				"System Telemetry Context:\n"+
				"%s\n\n"+
				"Provide a short, direct natural-language Root Cause Analysis log (max 2 sentences) identifying "+
				"the active scenario and the primary symptom. Start with '[RCA] '.",
			latestContext,
		)

		// Call localhost:8080/v1/chat/completions
		payloadMap := map[string]interface{}{
			"model": "qwen2.5:0.5b",
			"messages": []map[string]string{
				{"role": "user", "content": strings.TrimSpace(prompt)},
			},
		}
		payloadBytes, err := json.Marshal(payloadMap)
		if err != nil {
			slog.Error("Failed to marshal RCA completions payload", "error", err)
			rcaText = fmt.Sprintf("[RCA] Anomalies detected: %s", strings.Join(anomalies, "; "))
		} else {
			url := CompletionsURL + "/v1/chat/completions"
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(payloadBytes))
			if err != nil {
				slog.Error("Failed to build request to completions proxy", "error", err)
				rcaText = fmt.Sprintf("[RCA] Anomalies detected: %s", strings.Join(anomalies, "; "))
			} else {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Gateway-Internal", "true")
				resp, err := ollamaClient.Do(req)
				if err != nil {
					slog.Error("Failed to query completions proxy", "error", err)
					rcaText = fmt.Sprintf("[RCA] Anomalies detected: %s (Go proxy unreachable)", strings.Join(anomalies, "; "))
				} else {
					defer resp.Body.Close()
					respBody, err := io.ReadAll(resp.Body)
					if err != nil || resp.StatusCode != http.StatusOK {
						slog.Error("Go proxy returned error for RCA query", "status", resp.StatusCode)
						rcaText = fmt.Sprintf("[RCA] Anomalies detected: %s", strings.Join(anomalies, "; "))
					} else {
						var chatResp ChatCompletionResponse
						err = json.Unmarshal(respBody, &chatResp)
						if err == nil && len(chatResp.Choices) > 0 {
							rcaText = chatResp.Choices[0].Message.Content
						} else {
							rcaText = fmt.Sprintf("[RCA] Anomalies detected: %s", strings.Join(anomalies, "; "))
						}
					}
				}
			}
		}
	} else {
		rcaText = "INFO: Nominal system health. No anomalies detected."
	}

	f, err := os.OpenFile(RCALogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Error("Failed to open RCA log file", "error", err)
		return
	}
	defer f.Close()

	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	logEntry := fmt.Sprintf("[%s] %s\n", timestamp, strings.TrimSpace(rcaText))
	slog.Info("RCA DIAGNOSTICS", "entry", strings.TrimSpace(rcaText))
	_, _ = f.WriteString(logEntry)
}
