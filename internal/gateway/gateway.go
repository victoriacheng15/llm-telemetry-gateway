package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var (
	SocketPath      = "/tmp/shared/policy.sock"
	DiagnosticsPath = "/tmp/shared/diagnostics.txt"
	RCALogPath      = "/tmp/shared/rca.log"
	SigChan         = make(chan os.Signal, 1)

	meter         = otel.Meter("gateway")
	inputCounter  metric.Int64Counter
	outputCounter metric.Int64Counter
	durationHist  metric.Float64Histogram

	execCommand = exec.Command

	cpuStatPath      = "/sys/fs/cgroup/cpu.stat"
	cpuacctUsagePath = "/sys/fs/cgroup/cpuacct/cpuacct.usage"
	procStatPath     = "/proc/self/stat"
	memCurrentPath   = "/sys/fs/cgroup/memory.current"
	memUsagePath     = "/sys/fs/cgroup/memory/memory.usage_in_bytes"
	procStatusPath   = "/proc/self/status"
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

var globalTracker = &MetricsTracker{}

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
		lastReq := t.requests[len(t.requests)-1]
		currentAvg = lastReq.Duration * 1000
		currentP95 = lastReq.Duration * 1000
		currentP99 = lastReq.Duration * 1000
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

func HandleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	rangeStr := r.URL.Query().Get("range")
	duration := 15 * time.Minute
	switch rangeStr {
	case "30m":
		duration = 30 * time.Minute
	case "1h":
		duration = time.Hour
	case "3h":
		duration = 3 * time.Hour
	}

	metrics := globalTracker.GetMetrics(duration)
	json.NewEncoder(w).Encode(metrics)
}

func init() {
	InitMetrics()
}

func initLogger() {
	levelStr := os.Getenv("LOG_LEVEL")
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
}

func InitMetrics() {
	var err error
	inputCounter, err = meter.Int64Counter("gen_ai.usage.input_tokens",
		metric.WithDescription("Number of input tokens processed"),
	)
	if err != nil {
		slog.Warn("Failed to create inputCounter", "error", err)
	}

	outputCounter, err = meter.Int64Counter("gen_ai.usage.output_tokens",
		metric.WithDescription("Number of output tokens processed"),
	)
	if err != nil {
		slog.Warn("Failed to create outputCounter", "error", err)
	}

	durationHist, err = meter.Float64Histogram("gen_ai.client.request.duration_histogram",
		metric.WithDescription("Duration of client request in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		slog.Warn("Failed to create durationHist", "error", err)
	}
}

func Run(serverAddr string) {
	initLogger()

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	go globalTracker.StartCollector(bgCtx)

	// Initialize OpenTelemetry SDK
	ctx := context.Background()
	shutdown, err := initMeter(ctx)
	if err != nil {
		slog.Warn("OpenTelemetry meter initialization failed", "error", err)
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdown(shutdownCtx); err != nil {
				slog.Warn("OpenTelemetry shutdown failed", "error", err)
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", HandleCompletions)
	mux.HandleFunc("/healthz", HandleHealthz)
	mux.HandleFunc("/readyz", HandleReadyz)
	mux.HandleFunc("/api/chaos/stress", HandleChaosStress)
	mux.HandleFunc("/api/chaos/network", HandleChaosNetwork)
	mux.HandleFunc("/api/diagnostics", HandleDiagnostics)
	mux.HandleFunc("/api/logs/stream", HandleRCALogStream)
	mux.HandleFunc("/api/mask", HandleMaskTest)
	mux.HandleFunc("/api/limits", HandleLimits)
	mux.HandleFunc("/api/metrics", HandleMetrics)
	mux.Handle("/console/", http.StripPrefix("/console/", http.FileServer(http.Dir("internal/web/console"))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/console/", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:    serverAddr,
		Handler: mux,
	}

	signal.Notify(SigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Go Completions Proxy listening", "addr", serverAddr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("HTTP server failed to start or serve", "error", err)
			os.Exit(1)
		}
	}()

	<-SigChan
	slog.Info("Shutdown signal received. Stopping server...")
	ctxGraceful, cancelGraceful := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelGraceful()
	if err := srv.Shutdown(ctxGraceful); err != nil {
		slog.Error("Server graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Server stopped cleanly.")
}

func HandleCompletions(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	slog.Debug("Received chat completions request", "method", r.Method, "path", r.URL.Path)

	if r.Method != http.MethodPost {
		slog.Warn("Method not allowed", "method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Failed to read body"}`))
		return
	}

	payload := string(body) + "\n"

	// Record input tokens
	inputTokens := CountTokens(payload)
	if inputCounter != nil {
		inputCounter.Add(r.Context(), inputTokens)
	}

	response, err := DialAndSend(SocketPath, payload)
	if err != nil {
		slog.Error("PII policy engine unreachable", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "PII policy engine unreachable"}`))
		return
	}

	// Record output tokens
	outputTokens := CountTokens(response)
	if outputCounter != nil {
		outputCounter.Add(r.Context(), outputTokens)
	}

	// Record duration histogram
	duration := time.Since(startTime).Seconds()
	if durationHist != nil {
		durationHist.Record(r.Context(), duration)
	}

	globalTracker.RecordRequest(duration, inputTokens+outputTokens)

	slog.Info("Request completed successfully",
		"duration_seconds", duration,
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"status", http.StatusOK,
	)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(response))
}

// DialAndSend connects to the UDS socket (with retry), writes the payload, and returns the response.
func DialAndSend(path, payload string) (string, error) {
	conn, err := DialWithRetry(path, 5, 100*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("failed to dial UDS socket %s: %w", path, err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte(payload))
	if err != nil {
		return "", fmt.Errorf("failed to write to UDS socket: %w", err)
	}

	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read response from UDS socket: %w", err)
	}

	return response, nil
}

func DialWithRetry(path string, attempts int, backoff time.Duration) (net.Conn, error) {
	var conn net.Conn
	var err error
	for i := 0; i < attempts; i++ {
		conn, err = net.Dial("unix", path)
		if err == nil {
			return conn, nil
		}
		time.Sleep(backoff)
	}
	return nil, err
}

func CountTokens(text string) int64 {
	words := strings.Fields(text)
	if len(words) == 0 {
		return 1
	}
	return int64(len(words))
}

func initMeter(ctx context.Context) (func(context.Context) error, error) {
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("llm-telemetry-gateway"),
		),
	)
	if err != nil {
		return nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(meterProvider)

	return func(shutdownCtx context.Context) error {
		return meterProvider.Shutdown(shutdownCtx)
	}, nil
}

func HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "healthy"}`))
}

func HandleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Test dialing the UDS socket to verify readiness
	conn, err := net.DialTimeout("unix", SocketPath, 100*time.Millisecond)
	if err != nil {
		slog.Warn("Readiness check failed: PII policy engine unreachable", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(fmt.Sprintf(`{"status": "unready", "reason": "PII policy engine unreachable: %v"}`, err)))
		return
	}
	conn.Close()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "ready"}`))
}

func HandleChaosStress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var cmdArgs []string
	if r.Method == http.MethodPost {
		cmdArgs = []string{"apply", "-f", "/app/k3s/chaos-mesh/node-stress.yaml"}
	} else if r.Method == http.MethodDelete {
		cmdArgs = []string{"delete", "-f", "/app/k3s/chaos-mesh/node-stress.yaml"}
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cmd := execCommand("kubectl", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("Failed to manage stress chaos", "error", err, "output", string(out))
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error": "%s", "output": %q}`, err.Error(), string(out))
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "success", "output": %q}`, string(out))
}

func HandleChaosNetwork(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var cmdArgs []string
	if r.Method == http.MethodPost {
		cmdArgs = []string{"apply", "-f", "/app/k3s/chaos-mesh/network-delay.yaml"}
	} else if r.Method == http.MethodDelete {
		cmdArgs = []string{"delete", "-f", "/app/k3s/chaos-mesh/network-delay.yaml"}
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cmd := execCommand("kubectl", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("Failed to manage network chaos", "error", err, "output", string(out))
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error": "%s", "output": %q}`, err.Error(), string(out))
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status": "success", "output": %q}`, string(out))
}

func HandleDiagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	data, err := os.ReadFile(DiagnosticsPath)
	if err != nil {
		if os.IsNotExist(err) {
			w.Write([]byte("Diagnostics not yet available."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}

	w.Write(data)
}

func HandleLimits(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	cpuLimit := os.Getenv("LIMITS_CPU")
	if cpuLimit == "" {
		cpuLimit = "500m"
	}
	memLimit := os.Getenv("LIMITS_MEMORY")
	if memLimit == "" {
		memLimit = "512Mi"
	}

	response := map[string]string{
		"cpu":    cpuLimit,
		"memory": memLimit,
	}

	json.NewEncoder(w).Encode(response)
}

func HandleRCALogStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	file, err := os.Open(RCALogPath)
	if err != nil {
		file, err = os.OpenFile(RCALogPath, os.O_CREATE|os.O_RDONLY, 0644)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		fmt.Fprintf(w, "data: %s\n\n", strings.TrimSuffix(line, "\n"))
		flusher.Flush()
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			for {
				line, err := reader.ReadString('\n')
				if err == io.EOF {
					break
				}
				if err != nil {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", strings.TrimSuffix(line, "\n"))
				flusher.Flush()
			}
		}
	}
}

type MaskRequest struct {
	Prompt string `json:"prompt"`
}

type MaskResponse struct {
	Masked string `json:"masked"`
}

func HandleMaskTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req MaskRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "Invalid request JSON"}`))
		return
	}

	prompt := req.Prompt
	if !strings.HasSuffix(prompt, "\n") {
		prompt += "\n"
	}

	startTime := time.Now()
	response, err := DialAndSend(SocketPath, prompt)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"error": "PII policy engine unreachable: %v"}`, err)
		return
	}

	duration := time.Since(startTime).Seconds()
	tokens := CountTokens(prompt) + CountTokens(response)
	globalTracker.RecordRequest(duration, tokens)

	res := MaskResponse{
		Masked: strings.TrimSuffix(response, "\n"),
	}
	json.NewEncoder(w).Encode(res)
}
