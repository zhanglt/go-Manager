package dashboard

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

type detailsResource struct {
	name  string
	path  []string
	query url.Values
}

type detailsFetch struct {
	value  map[string]any
	error  map[string]any
	status int
}

func (h *Handler) GetDetails(c *gin.Context) {
	domain := c.Query("domain")
	resources := []detailsResource{
		{"hosts", []string{"host"}, detailsQuery(url.Values{"start": {"0"}, "limit": {"0"}}, domain)},
		{"groups", []string{"group"}, detailsQuery(url.Values{"view": {"pod"}, "scope": {"local"}}, domain)},
		{"rules", []string{"policy", "rule"}, detailsDomainQuery(domain)},
		{"conversations", []string{"conversation"}, detailsDomainQuery(domain)},
		{"workloads", []string{"workload"}, url.Values{"view": {"pod"}}},
		{"scan", []string{"scan", "config"}, nil},
	}
	values := make(map[string]map[string]any, len(resources))
	errors := make(map[string]map[string]any)
	statuses := make(map[string]int)
	var mu sync.Mutex
	var wait sync.WaitGroup
	for _, resource := range resources {
		resource := resource
		wait.Add(1)
		go func() {
			defer wait.Done()
			result := h.fetchDetails(c, resource)
			mu.Lock()
			defer mu.Unlock()
			values[resource.name] = result.value
			if result.error != nil {
				errors[resource.name] = result.error
			}
			if result.status != 0 {
				statuses[resource.name] = result.status
			}
		}()
	}
	wait.Wait()
	for _, resource := range resources {
		if status := statuses[resource.name]; status != 0 {
			writeDetailsFailure(c, status)
			return
		}
	}
	c.Data(http.StatusOK, "application/json", mustDetailsJSON(buildDetails(values, errors, domain)))
}

func (h *Handler) fetchDetails(c *gin.Context, resource detailsResource) detailsFetch {
	headers := make(http.Header)
	headers.Set("X-Nv-Page", "dashboard")
	response, err := h.transfer.Fetch(c, http.MethodGet, resource.query, headers, nil, resource.path...)
	if err != nil {
		return detailsFetch{value: detailsDefault(resource.name), error: detailsError(err.Error())}
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusServiceUnavailable {
			return detailsFetch{value: detailsDefault(resource.name), status: response.StatusCode}
		}
		result := detailsFetch{value: detailsDefault(resource.name)}
		if response.StatusCode < 400 || response.StatusCode >= 500 {
			result.error = detailsError("Status: " + response.Status)
		}
		return result
	}
	defer response.Body.Close()
	var reader io.Reader = response.Body
	if strings.Contains(strings.ToLower(response.Header.Get("Content-Encoding")), "gzip") {
		decoded, err := gzip.NewReader(response.Body)
		if err != nil {
			return detailsFetch{value: detailsDefault(resource.name), error: detailsError(err.Error())}
		}
		defer decoded.Close()
		reader = decoded
	}
	var output map[string]any
	if err := json.NewDecoder(io.LimitReader(reader, h.maxBytes+1)).Decode(&output); err != nil {
		return detailsFetch{status: http.StatusInternalServerError}
	}
	return detailsFetch{value: output}
}

func detailsDefault(name string) map[string]any {
	switch name {
	case "hosts":
		return map[string]any{"hosts": []any{}}
	case "groups":
		return map[string]any{"groups": []any{}}
	case "rules":
		return map[string]any{"rules": []any{}}
	case "workloads":
		return map[string]any{"workloads": []any{}}
	case "conversations":
		return map[string]any{"endpoints": []any{}, "conversations": []any{}}
	default:
		return map[string]any{"config": map[string]any{"auto_scan": false}}
	}
}

func detailsError(message string) map[string]any { return map[string]any{"message": message} }

func writeDetailsFailure(c *gin.Context, status int) {
	switch status {
	case http.StatusUnauthorized:
		c.String(status, "Authentication failed!")
	case http.StatusRequestTimeout:
		c.String(status, "Session expired!")
	case http.StatusServiceUnavailable:
		c.String(status, "Server is not available!")
	default:
		c.String(http.StatusInternalServerError, "Internal server error")
	}
}

func detailsQuery(base url.Values, domain string) url.Values {
	if domain != "" {
		base.Set("f_domain", domain)
	}
	return base
}
func detailsDomainQuery(domain string) url.Values {
	if domain == "" {
		return nil
	}
	return url.Values{"f_domain": {domain}}
}
func mustDetailsJSON(value any) []byte { output, _ := json.Marshal(value); return output }

func buildDetails(source, failures map[string]map[string]any, domain string) map[string]any {
	groups := objects(source["groups"]["groups"])
	rules := objects(source["rules"]["rules"])
	workloads := objects(source["workloads"]["workloads"])
	hosts := objects(source["hosts"]["hosts"])
	conversations := objects(source["conversations"]["conversations"])
	underRules := map[string]bool{}
	applications := map[string]int{}
	for _, rule := range rules {
		for _, side := range []string{"from", "to"} {
			if value := text(rule[side]); strings.HasPrefix(value, "nv.") {
				underRules[strings.TrimPrefix(value, "nv.")] = true
			}
		}
		for _, app := range stringsOf(rule["applications"]) {
			applications[app]++
		}
	}
	serviceMap := map[string]map[string]any{}
	learnt, others, services := []map[string]any{}, []map[string]any{}, []map[string]any{}
	for _, group := range groups {
		if truth(group["not_scored"]) || text(group["platform_role"]) != "" || text(group["kind"]) == "node" {
			continue
		}
		services = append(services, group)
		members := objects(group["members"])
		if len(members) == 0 {
			continue
		}
		name := text(group["name"])
		serviceMap[name] = group
		if underRules[name] {
			learnt = append(learnt, group)
		} else {
			others = append(others, group)
		}
	}
	running := []map[string]any{}
	for _, workload := range workloads {
		if text(workload["state"]) == "exit" || (domain != "" && text(workload["domain"]) != domain) {
			continue
		}
		if _, ok := serviceMap["nv."+text(workload["service"])]; ok {
			running = append(running, workload)
		}
	}
	appBytes := map[string]struct {
		Count int
		Bytes int64
	}{}
	for _, conversation := range conversations {
		conversationApps := stringsOf(conversation["applications"])
		if conversation["applications"] == nil {
			conversationApps = []string{"Others"}
		}
		for _, app := range conversationApps {
			item := appBytes[app]
			item.Count++
			item.Bytes += integer(conversation["bytes"])
			appBytes[app] = item
		}
	}
	applications2 := make([]any, 0, len(appBytes))
	for app, data := range appBytes {
		applications2 = append(applications2, []any{app, map[string]any{"count": data.Count, "totalBytes": data.Bytes}})
	}
	sort.Slice(applications2, func(i, j int) bool {
		return applications2[i].([]any)[0].(string) < applications2[j].([]any)[0].(string)
	})
	policyApps := make([]any, 0, len(applications))
	for app, count := range applications {
		policyApps = append(policyApps, []any{app, count})
	}
	sort.Slice(policyApps, func(i, j int) bool {
		a, b := policyApps[i].([]any), policyApps[j].([]any)
		if a[1].(int) == b[1].(int) {
			return a[0].(string) < b[0].(string)
		}
		return a[1].(int) > b[1].(int)
	})
	high := highPriority(workloads, hosts, serviceMap, domain)
	auto := false
	if config, ok := source["scan"]["config"].(map[string]any); ok {
		auto = truth(config["auto_scan"])
	}
	result := map[string]any{"highPriorityVulnerabilities": high, "containers": running, "services": services, "applications": policyApps, "applications2": applications2, "policyCoverage": map[string]any{"learnt": learnt, "others": others}, "autoScanConfig": auto}
	applyDetailsFailures(result, failures)
	return cleanDetailsNulls(result).(map[string]any)
}

func applyDetailsFailures(result map[string]any, failures map[string]map[string]any) {
	serviceFailure := failures["groups"]
	if serviceFailure == nil {
		serviceFailure = failures["rules"]
	}
	containerFailure := failures["workloads"]
	if containerFailure == nil {
		containerFailure = serviceFailure
	}
	if serviceFailure != nil {
		result["highPriorityVulnerabilities"] = serviceFailure
		result["services"] = serviceFailure
		result["policyCoverage"] = serviceFailure
	}
	if containerFailure != nil {
		result["containers"] = containerFailure
	}
	if failure := failures["rules"]; failure != nil {
		result["applications"] = failure
	}
	conversationFailure := failures["conversations"]
	if conversationFailure == nil {
		conversationFailure = containerFailure
	}
	if conversationFailure != nil {
		result["applications2"] = conversationFailure
	}
	if failure := failures["scan"]; failure != nil {
		result["autoScanConfig"] = failure
	}
	if serviceFailure == nil {
		high := result["highPriorityVulnerabilities"].(map[string]any)
		if failure := failures["workloads"]; failure != nil {
			high["containers"] = failure
		}
		if failure := failures["hosts"]; failure != nil {
			high["nodes"] = failure
		}
	}
}

func highPriority(workloads, hosts []map[string]any, services map[string]map[string]any, domain string) map[string]any {
	items := []map[string]any{}
	total := 0
	for _, item := range workloads {
		if text(item["state"]) == "exit" || text(item["platform_role"]) != "" || (domain != "" && text(item["domain"]) != domain) {
			continue
		}
		if len(services) > 0 {
			if _, ok := services["nv."+text(item["service"])]; !ok {
				continue
			}
		}
		total++
		copy := clone(item)
		c, h, m := scanCounts(copy)
		copy["critical4Dashboard"], copy["high4Dashboard"], copy["medium4Dashboard"] = c, h, m
		if h != 0 || m != 0 {
			items = append(items, copy)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if integer(a["critical4Dashboard"]) != integer(b["critical4Dashboard"]) {
			return integer(a["critical4Dashboard"]) > integer(b["critical4Dashboard"])
		}
		if integer(a["high4Dashboard"]) != integer(b["high4Dashboard"]) {
			return integer(a["high4Dashboard"]) > integer(b["high4Dashboard"])
		}
		return integer(a["medium4Dashboard"]) > integer(b["medium4Dashboard"])
	})
	nodes := []map[string]any{}
	for _, node := range hosts {
		_, h, m := scanCounts(node)
		if h != 0 || m != 0 {
			nodes = append(nodes, node)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		_, ah, am := scanCounts(nodes[i])
		_, bh, bm := scanCounts(nodes[j])
		if ah != bh {
			return ah > bh
		}
		return am > bm
	})
	return map[string]any{"containers": map[string]any{"top5Containers": limitDetails(items, 5), "vulnerabilitiesTotal": len(items), "total": total}, "nodes": map[string]any{"top5Nodes": limitDetails(nodes, 5), "vulnerabilitiesTotal": len(nodes)}}
}

func objects(value any) []map[string]any {
	values, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		if item, ok := v.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}
func stringsOf(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func text(value any) string { output, _ := value.(string); return output }
func truth(value any) bool  { output, _ := value.(bool); return output }
func integer(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}
func clone(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for k, v := range value {
		out[k] = v
	}
	return out
}
func scanCounts(value map[string]any) (int64, int64, int64) {
	summary, _ := value["scan_summary"].(map[string]any)
	c, h, m := integer(summary["critical"]), integer(summary["high"]), integer(summary["medium"])
	for _, child := range objects(value["children"]) {
		if text(child["state"]) != "exit" {
			x, y, z := scanCounts(child)
			c += x
			h += y
			m += z
		}
	}
	return c, h, m
}
func limitDetails[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

func cleanDetailsNulls(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if item == nil {
				delete(current, key)
				continue
			}
			current[key] = cleanDetailsNulls(item)
		}
	case []map[string]any:
		for index := range current {
			current[index] = cleanDetailsNulls(current[index]).(map[string]any)
		}
	case []any:
		for index := range current {
			current[index] = cleanDetailsNulls(current[index])
		}
	}
	return value
}
