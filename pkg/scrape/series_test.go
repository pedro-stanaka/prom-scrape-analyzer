package scrape_test

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	"github.com/pedro-stanaka/prom-scrape-analyzer/pkg/scrape"
)

func TestSeriesSet_Cardinality(t *testing.T) {
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1"},
		2: {Name: "series2"},
	}

	expected := 2
	require.Equal(t, expected, seriesSet.Cardinality(), "Cardinality() should return the correct number of series")
}

func TestSeriesSet_MetricTypeString(t *testing.T) {
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", Type: "gauge"},
		2: {Name: "series2", Type: "counter"},
	}

	expected := "gauge|counter"
	require.Equal(t, expected, seriesSet.MetricTypeString(), "MetricTypeString() should return the correct metric types")
}

func TestSeriesSet_CreatedTS(t *testing.T) {
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", CreatedTimestamp: 1620000000},
		2: {Name: "series2", CreatedTimestamp: 1620000001},
	}

	expected := int64(1620000000)
	require.Equal(t, expected, seriesSet.CreatedTS(), "CreatedTS() should return the correct created timestamp")
}

func TestSeriesSet_LabelNames(t *testing.T) {
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", Labels: labels.Labels{{Name: "label1"}, {Name: "label2"}}},
		2: {Name: "series2", Labels: labels.Labels{{Name: "label2"}, {Name: "label3"}}},
	}

	expected := "label1|label2|label3"
	require.Equal(t, expected, seriesSet.LabelNames(), "LabelNames() should return the correct label names")
}

func TestSeriesSet_LabelStats(t *testing.T) {
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", Labels: labels.Labels{{Name: "label1", Value: "foo"}, {Name: "label2", Value: "bar"}}},
		2: {Name: "series2", Labels: labels.Labels{{Name: "label2", Value: "baz"}, {Name: "label3", Value: "qux"}}},
		3: {Name: "series3", Labels: labels.Labels{{Name: "label2", Value: "baz"}, {Name: "label3", Value: "qua"}}},
	}

	expected := scrape.LabelStatsSlice{
		{Name: "label1", DistinctValues: 1},
		{Name: "label2", DistinctValues: 2},
		{Name: "label3", DistinctValues: 2},
	}
	got := seriesSet.LabelStats()

	require.Len(t, got, len(expected), "LabelStats() should return the correct number of label stats")
	require.EqualValues(t, expected, got, "LabelStats() should return the correct label stats")
}
