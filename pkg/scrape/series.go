package scrape

import (
	"slices"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

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

func (s SeriesSet) CreatedTS() int64 {
	for _, v := range s {
		return v.CreatedTimestamp
	}
	return 0
}

func (s SeriesSet) LabelNames() string {
	if len(s) == 0 {
		return ""
	}
	labelSet := make(map[string]struct{})
	for _, v := range s {
		for _, l := range v.Labels {
			if l.Name != "__name__" {
				labelSet[l.Name] = struct{}{}
			}
		}
	}
	lbls := make([]string, 0, len(labelSet))
	for label := range labelSet {
		lbls = append(lbls, label)
	}
	return strings.Join(lbls, "|")
}

type SeriesMap map[string]SeriesSet

type Result struct {
	Series          SeriesMap
	UsedContentType string
}

type SeriesInfo struct {
	Name        string
	Cardinality int
	Type        string
	Labels      string
	CreatedTS   string
}

func (s SeriesMap) AsRows() []SeriesInfo {
	var rows []SeriesInfo
	for name, s := range s {
		createdTs := int64(0)
		if len(s) > 0 {
			createdTs = int64(int(s.CreatedTS()))
		}
		createdTsStr := "_empty_"
		if createdTs > 0 {
			createdTsStr = time.UnixMilli(createdTs).String()
		}
		rows = append(rows, SeriesInfo{
			Name:        name,
			Cardinality: s.Cardinality(),
			Type:        s.MetricTypeString(),
			Labels:      s.LabelNames(),
			CreatedTS:   createdTsStr,
		})
	}

	slices.SortFunc(rows, func(i, j SeriesInfo) int { return (i.Cardinality - j.Cardinality) * -1 })

	return rows
}
