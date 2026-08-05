package observability

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var latencyBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

type requestKey struct {
	method string
	route  string
	status int
}

type histogram struct {
	count   uint64
	sum     float64
	buckets [len(latencyBuckets)]uint64
}

type CacheSnapshot struct {
	Entries           int
	Bytes             int64
	CapacityEntries   int
	CapacityBytes     int64
	CapacityEvictions uint64
}

type Registry struct {
	mu sync.RWMutex

	requests       map[requestKey]uint64
	requestLatency map[string]*histogram
	loginFailures  map[string]uint64
	controller     map[string]uint64
	controllerFail map[string]uint64
	controllerUp   bool
	controllerSeen bool
	caches         map[string]func() CacheSnapshot
	session        func() (int, int)
	support        map[string]uint64
	supportRunning int64
	supportLatency histogram
	supportBytes   uint64
}

func NewRegistry() *Registry {
	return &Registry{
		requests: make(map[requestKey]uint64), requestLatency: make(map[string]*histogram),
		loginFailures: make(map[string]uint64), controller: make(map[string]uint64),
		controllerFail: make(map[string]uint64), caches: make(map[string]func() CacheSnapshot),
		support: make(map[string]uint64),
	}
}

func (r *Registry) ObserveRequest(method, route string, status int, elapsed time.Duration) {
	method = normalizeMethod(method)
	if route == "" {
		route = "unmatched"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests[requestKey{method: method, route: route, status: status}]++
	observeHistogram(r.requestHistogram(method, route), elapsed.Seconds())
	if status >= http.StatusBadRequest && isLoginRoute(route) {
		r.loginFailures[route]++
	}
}

func (r *Registry) ObserveController(method string, status int, elapsed time.Duration, err error) {
	method = normalizeMethod(method)
	result := strconv.Itoa(status)
	failure := ""
	if err != nil {
		result = "transport_error"
		failure = "transport"
	} else if status >= http.StatusInternalServerError {
		failure = "5xx"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.controller[method+"\x00"+result]++
	observeHistogram(r.requestHistogram("controller", method), elapsed.Seconds())
	r.controllerSeen = true
	r.controllerUp = err == nil && status < http.StatusInternalServerError
	if failure != "" {
		r.controllerFail[failure]++
	}
}

func (r *Registry) RegisterCache(name string, snapshot func() CacheSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.caches[name] = snapshot
}

func (r *Registry) RegisterSession(snapshot func() (int, int)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.session = snapshot
}

func (r *Registry) SupportStarted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.supportRunning++
}

func (r *Registry) SupportFinished(outcome string, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.supportRunning > 0 {
		r.supportRunning--
	}
	r.support[outcome]++
	observeHistogram(&r.supportLatency, elapsed.Seconds())
}

func (r *Registry) SupportRejected(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.support[reason]++
}

func (r *Registry) SupportDownloaded(bytes int64) {
	if bytes < 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.supportBytes += uint64(bytes)
}

func (r *Registry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(r.render()))
}

func (r *Registry) render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var output strings.Builder
	writeHelp(&output, "manager_http_requests_total", "Manager HTTP requests by method, normalized route, and status.", "counter")
	requestKeys := make([]requestKey, 0, len(r.requests))
	for key := range r.requests {
		requestKeys = append(requestKeys, key)
	}
	sort.Slice(requestKeys, func(i, j int) bool {
		return fmt.Sprint(requestKeys[i]) < fmt.Sprint(requestKeys[j])
	})
	for _, key := range requestKeys {
		fmt.Fprintf(&output, "manager_http_requests_total{method=%q,route=%q,status=%q} %d\n", key.method, key.route, strconv.Itoa(key.status), r.requests[key])
	}
	writeHistogramMap(&output, "manager_http_request_duration_seconds", "Manager HTTP request latency.", r.requestLatency)
	writeStringCounter(&output, "manager_login_failures_total", "Failed Manager login requests.", "route", r.loginFailures)
	writePairCounter(&output, "manager_controller_requests_total", "Requests sent to Controller.", "method", "status", r.controller)
	writeStringCounter(&output, "manager_controller_failures_total", "Failed Controller requests.", "reason", r.controllerFail)
	writeHelp(&output, "manager_controller_available", "Whether the most recent Controller request succeeded.", "gauge")
	if r.controllerSeen {
		available := 0
		if r.controllerUp {
			available = 1
		}
		fmt.Fprintf(&output, "manager_controller_available %d\n", available)
	}
	r.writeCapacityMetrics(&output)
	writeStringCounter(&output, "manager_support_operations_total", "Support operations by outcome.", "outcome", r.support)
	writeHelp(&output, "manager_support_operations_running", "Currently running support commands.", "gauge")
	fmt.Fprintf(&output, "manager_support_operations_running %d\n", r.supportRunning)
	writeHistogram(&output, "manager_support_operation_duration_seconds", "Support command duration.", nil, &r.supportLatency)
	writeHelp(&output, "manager_support_download_bytes_total", "Bytes served from completed support archives.", "counter")
	fmt.Fprintf(&output, "manager_support_download_bytes_total %d\n", r.supportBytes)
	writeRuntimeMetrics(&output)
	return output.String()
}

func (r *Registry) writeCapacityMetrics(output *strings.Builder) {
	for _, metric := range []struct{ name, help string }{
		{"manager_cache_entries", "Current cache entry count."},
		{"manager_cache_bytes", "Current cache payload bytes."},
		{"manager_cache_capacity_entries", "Configured cache entry capacity."},
		{"manager_cache_capacity_bytes", "Configured cache byte capacity."},
		{"manager_cache_evictions_total", "Cache entries evicted to enforce capacity."},
	} {
		metricType := "gauge"
		if strings.HasSuffix(metric.name, "_total") {
			metricType = "counter"
		}
		writeHelp(output, metric.name, metric.help, metricType)
	}
	names := make([]string, 0, len(r.caches))
	for name := range r.caches {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := r.caches[name]()
		fmt.Fprintf(output, "manager_cache_entries{cache=%q} %d\n", name, value.Entries)
		fmt.Fprintf(output, "manager_cache_bytes{cache=%q} %d\n", name, value.Bytes)
		fmt.Fprintf(output, "manager_cache_capacity_entries{cache=%q} %d\n", name, value.CapacityEntries)
		fmt.Fprintf(output, "manager_cache_capacity_bytes{cache=%q} %d\n", name, value.CapacityBytes)
		fmt.Fprintf(output, "manager_cache_evictions_total{cache=%q} %d\n", name, value.CapacityEvictions)
	}
	if r.session != nil {
		entries, capacity := r.session()
		writeHelp(output, "manager_sessions", "Current in-process sessions.", "gauge")
		fmt.Fprintf(output, "manager_sessions %d\n", entries)
		writeHelp(output, "manager_session_capacity", "Configured in-process session capacity.", "gauge")
		fmt.Fprintf(output, "manager_session_capacity %d\n", capacity)
	}
}

func (r *Registry) requestHistogram(first, second string) *histogram {
	key := first + "\x00" + second
	value := r.requestLatency[key]
	if value == nil {
		value = &histogram{}
		r.requestLatency[key] = value
	}
	return value
}

func observeHistogram(target *histogram, value float64) {
	target.count++
	target.sum += value
	for index, bucket := range latencyBuckets {
		if value <= bucket {
			target.buckets[index]++
		}
	}
}

func writeHistogramMap(output *strings.Builder, name, help string, values map[string]*histogram) {
	keys := make([]string, 0, len(values))
	for key := range values {
		if !strings.HasPrefix(key, "controller\x00") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		writeHelp(output, name, help, "histogram")
	}
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		writeHistogramSamples(output, name, map[string]string{"method": parts[0], "route": parts[1]}, values[key])
	}
	controller := make(map[string]*histogram)
	for key, value := range values {
		if strings.HasPrefix(key, "controller\x00") {
			controller[strings.TrimPrefix(key, "controller\x00")] = value
		}
	}
	if len(controller) > 0 {
		writeHelp(output, "manager_controller_request_duration_seconds", "Controller request latency.", "histogram")
		methods := make([]string, 0, len(controller))
		for method := range controller {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		for _, method := range methods {
			writeHistogramSamples(output, "manager_controller_request_duration_seconds", map[string]string{"method": method}, controller[method])
		}
	}
}

func writeHistogram(output *strings.Builder, name, help string, labels map[string]string, value *histogram) {
	writeHelp(output, name, help, "histogram")
	writeHistogramSamples(output, name, labels, value)
}

func writeHistogramSamples(output *strings.Builder, name string, labels map[string]string, value *histogram) {
	for index, bucket := range latencyBuckets {
		fmt.Fprintf(output, "%s_bucket%s %d\n", name, labelsText(labels, "le", strconv.FormatFloat(bucket, 'g', -1, 64)), value.buckets[index])
	}
	fmt.Fprintf(output, "%s_bucket%s %d\n", name, labelsText(labels, "le", "+Inf"), value.count)
	fmt.Fprintf(output, "%s_sum%s %.9g\n", name, labelsText(labels, "", ""), value.sum)
	fmt.Fprintf(output, "%s_count%s %d\n", name, labelsText(labels, "", ""), value.count)
}

func writeStringCounter(output *strings.Builder, name, help, label string, values map[string]uint64) {
	writeHelp(output, name, help, "counter")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(output, "%s{%s=%q} %d\n", name, label, key, values[key])
	}
}

func writePairCounter(output *strings.Builder, name, help, first, second string, values map[string]uint64) {
	writeHelp(output, name, help, "counter")
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		fmt.Fprintf(output, "%s{%s=%q,%s=%q} %d\n", name, first, parts[0], second, parts[1], values[key])
	}
}

func labelsText(labels map[string]string, extraName, extraValue string) string {
	keys := make([]string, 0, len(labels)+1)
	for key := range labels {
		keys = append(keys, key)
	}
	if extraName != "" {
		keys = append(keys, extraName)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := labels[key]
		if key == extraName {
			value = extraValue
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, value))
	}
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func writeHelp(output *strings.Builder, name, help, metricType string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, metricType)
}

func writeRuntimeMetrics(output *strings.Builder) {
	writeHelp(output, "go_goroutines", "Number of goroutines that currently exist.", "gauge")
	fmt.Fprintf(output, "go_goroutines %d\n", runtime.NumGoroutine())
	writeHelp(output, "process_resident_memory_bytes", "Resident memory size in bytes.", "gauge")
	fmt.Fprintf(output, "process_resident_memory_bytes %d\n", residentMemoryBytes())
}

func residentMemoryBytes() uint64 {
	content, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

func isLoginRoute(route string) bool {
	for _, suffix := range []string{"/auth", "/token_auth_server", "/openId_auth"} {
		if route == suffix || strings.HasSuffix(route, suffix) {
			return true
		}
	}
	return false
}

func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}
