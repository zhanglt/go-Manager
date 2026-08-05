# Manager observability

The Go Manager exposes `/livez`, `/readyz`, and `/metrics` only on
`MANAGER_INTERNAL_ADDR`. Keep this listener behind a cluster network policy; do not publish it through
the user-facing ingress.

Install `prometheus-rules.yaml` in the deployment's Prometheus rule selector and import
`grafana-dashboard.json` into the operations Grafana folder. The initial thresholds are conservative
defaults. Before production rollout, owners must tune RSS against the container memory limit and tune
latency against the approved Scala baseline and release-candidate load test.

Prometheus labels are deliberately bounded to HTTP method, normalized Gin route, status, cache name,
Controller outcome, and support outcome. Tokens, credentials, user names, raw URLs, query strings,
cluster IDs, and file names are never labels.
