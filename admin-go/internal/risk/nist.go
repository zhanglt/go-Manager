package risk

import (
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type nistCompliance struct {
	Name       string `json:"name"`
	Subcontrol string `json:"subcontrol"`
	ControlID  string `json:"control_id"`
	Title      string `json:"title"`
}

type nistDatabase struct {
	once    sync.Once
	err     error
	records map[string]nistCompliance
}

func newNISTDatabase() *nistDatabase { return &nistDatabase{} }

func (d *nistDatabase) lookup(names []string) (map[string]nistCompliance, error) {
	d.once.Do(d.load)
	if d.err != nil {
		return nil, d.err
	}
	result := make(map[string]nistCompliance, len(names))
	for _, name := range names {
		if record, ok := d.records[name]; ok {
			result[name] = record
		}
	}
	return result, nil
}

func (d *nistDatabase) load() {
	file, err := os.Open(nistDatabasePath())
	if err != nil {
		d.err = err
		return
	}
	defer file.Close()
	reader := csv.NewReader(file)
	if _, err := reader.Read(); err != nil {
		d.err = err
		return
	}
	d.records = make(map[string]nistCompliance)
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil || len(row) < 6 {
			d.err = errors.New("invalid CIS NIST database")
			return
		}
		record := nistCompliance{Name: row[1], Subcontrol: row[3], ControlID: row[4], Title: row[5]}
		d.records[record.Name] = record
	}
}

func nistDatabasePath() string {
	if configured := os.Getenv("CIS_NIST_DB"); configured != "" {
		return configured
	}
	const name = "CIS_NIST-MASTER.CSV"
	for _, candidate := range []string{
		filepath.Join("admin", "src", "main", "resources", name),
		filepath.Join("..", "admin", "src", "main", "resources", name),
		filepath.Join("/usr/share/neuvector", name),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filepath.Join("/usr/share/neuvector", name)
}
