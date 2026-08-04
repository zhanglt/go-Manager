package notification

import (
	"encoding/csv"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type ipGeoRange struct {
	from, to [16]byte
	country  uint16
}

type ipGeoCountry struct{ code, name string }

type ipGeoResult struct {
	From        *big.Int `json:"from"`
	To          *big.Int `json:"to"`
	CountryCode string   `json:"country_code"`
	CountryName string   `json:"country_name"`
}

type ipGeoDatabase struct {
	once      sync.Once
	err       error
	v4, v6    []ipGeoRange
	countries []ipGeoCountry
}

func newIPGeoDatabase() *ipGeoDatabase { return &ipGeoDatabase{} }

func (d *ipGeoDatabase) lookup(addresses []string) (map[string]ipGeoResult, error) {
	d.once.Do(d.load)
	if d.err != nil {
		return nil, d.err
	}
	results := make(map[string]ipGeoResult, len(addresses))
	for _, address := range addresses {
		if _, exists := results[address]; !exists {
			results[address] = d.lookupOne(address)
		}
	}
	return results, nil
}

func (d *ipGeoDatabase) lookupOne(address string) ipGeoResult {
	ip := net.ParseIP(address)
	if ip == nil {
		return unknownIPGeo([16]byte{})
	}
	var key [16]byte
	ranges := d.v6
	if v4 := ip.To4(); v4 != nil {
		copy(key[12:], v4)
		ranges = d.v4
	} else {
		copy(key[:], ip.To16())
	}
	index := sort.Search(len(ranges), func(i int) bool { return compareIP(ranges[i].to, key) >= 0 })
	if index == len(ranges) || compareIP(ranges[index].from, key) > 0 {
		return unknownIPGeo(key)
	}
	record := ranges[index]
	country := d.countries[record.country]
	return ipGeoResult{ipNumber(record.from), ipNumber(record.to), country.code, country.name}
}

func unknownIPGeo(key [16]byte) ipGeoResult {
	value := ipNumber(key)
	return ipGeoResult{value, new(big.Int).Set(value), "-", "-"}
}

func (d *ipGeoDatabase) load() {
	d.countries = []ipGeoCountry{{"-", "-"}}
	countryIDs := map[ipGeoCountry]uint16{d.countries[0]: 0}
	for _, item := range []struct {
		path string
		v4   bool
	}{
		{databasePath("IP_GEO_IPV4_DB", "IP2LOCATION-LITE-DB1.CSV"), true},
		{databasePath("IP_GEO_IPV6_DB", "IP2LOCATION-LITE-DB1.IPV6.CSV"), false},
	} {
		ranges, err := loadIPGeoCSV(item.path, countryIDs, &d.countries)
		if err != nil {
			d.err = err
			return
		}
		if item.v4 {
			d.v4 = ranges
		} else {
			d.v6 = ranges
		}
	}
}

func databasePath(environment, name string) string {
	if configured := os.Getenv(environment); configured != "" {
		return configured
	}
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

func loadIPGeoCSV(path string, countryIDs map[ipGeoCountry]uint16, countries *[]ipGeoCountry) ([]ipGeoRange, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	var ranges []ipGeoRange
	for {
		row, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return ranges, nil
		}
		if err != nil || len(row) != 4 {
			return nil, errors.New("invalid IP geolocation database")
		}
		from, okFrom := decimalIP(row[0])
		to, okTo := decimalIP(row[1])
		if !okFrom || !okTo {
			return nil, errors.New("invalid IP geolocation range")
		}
		country := ipGeoCountry{row[2], row[3]}
		countryID, exists := countryIDs[country]
		if !exists {
			if len(*countries) >= 1<<16 {
				return nil, errors.New("too many IP geolocation countries")
			}
			countryID = uint16(len(*countries))
			countryIDs[country] = countryID
			*countries = append(*countries, country)
		}
		ranges = append(ranges, ipGeoRange{from, to, countryID})
	}
}

func decimalIP(value string) ([16]byte, bool) {
	var result [16]byte
	number, ok := new(big.Int).SetString(value, 10)
	if !ok || number.Sign() < 0 || number.BitLen() > 128 {
		return result, false
	}
	number.FillBytes(result[:])
	return result, true
}

func ipNumber(value [16]byte) *big.Int { return new(big.Int).SetBytes(value[:]) }

func compareIP(left, right [16]byte) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
