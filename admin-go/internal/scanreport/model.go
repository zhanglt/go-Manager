package scanreport

type Request struct {
	Cursor         *Cursor         `json:"cursor,omitempty"`
	Filters        *[]Filter       `json:"filters,omitempty"`
	MaxCVERecords  *int            `json:"max_cve_records,omitempty"`
	SeverityFilter *string         `json:"severity_filter,omitempty"`
	ShowAccepted   *bool           `json:"show_accepted,omitempty"`
	ViewPod        *string         `json:"view_pod,omitempty"`
	VulScoreFilter *VulScoreFilter `json:"vul_score_filter,omitempty"`
}

type Cursor struct {
	CVEName    *string `json:"cve_name,omitempty"`
	CVEPackage *string `json:"cve_package,omitempty"`
	Domain     *string `json:"domain,omitempty"`
	HostName   *string `json:"host_name,omitempty"`
	Name       *string `json:"name,omitempty"`
}

type VulScoreFilter struct {
	ScoreBottom  *int    `json:"score_bottom,omitempty"`
	ScoreTop     *int    `json:"score_top,omitempty"`
	ScoreVersion *string `json:"score_version,omitempty"`
}

type Filter struct {
	Name  *string   `json:"name,omitempty"`
	Op    *string   `json:"op,omitempty"`
	Value *[]string `json:"value,omitempty"`
}
