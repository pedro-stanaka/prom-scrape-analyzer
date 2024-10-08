package scrape

import "github.com/prometheus/prometheus/model/labels"

type Series struct {
	Name             string
	Labels           labels.Labels
	Type             string
	CreatedTimestamp int64
}

type SeriesSet map[uint64]Series

func (s SeriesSet) Cardinality() int {
	return len(s)
}

func (s SeriesSet) MetricTypeString() string {
	if len(s) == 0 {
		return ""
	}
	typeStr := ""
	lastType := ""
	for _, v := range s {
		if v.Type == "" {
			v.Type = "unknown"
		}
		if lastType != v.Type {
			if typeStr != "" {
				typeStr += "|"
			}
			typeStr += v.Type
			lastType = v.Type
		}
	}
	return typeStr
}
