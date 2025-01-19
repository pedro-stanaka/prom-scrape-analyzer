package scrape_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/require"

	"github.com/pedro-stanaka/prom-scrape-analyzer/pkg/scrape"
)

func TestSeriesSet_Cardinality(t *testing.T) {
	t.Parallel()
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1"},
		2: {Name: "series2"},
	}

	expected := 2
	require.Equal(t, expected, seriesSet.Cardinality(), "Cardinality() should return the correct number of series")
}

func TestSeriesSet_MetricTypeString(t *testing.T) {
	t.Parallel()
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", Type: "gauge"},
		2: {Name: "series2", Type: "counter"},
	}

	// Get actual result and sort it
	actual := seriesSet.MetricTypeString()
	actualParts := strings.Split(actual, "|")
	sort.Strings(actualParts)

	// Sort expected parts the same way
	expectedParts := []string{"gauge", "counter"}
	sort.Strings(expectedParts)

	require.Equal(t, strings.Join(expectedParts, "|"), strings.Join(actualParts, "|"),
		"MetricTypeString() should return the correct metric types")
}

func TestSeriesSet_CreatedTS(t *testing.T) {
	t.Parallel()
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", CreatedTimestamp: 1620000000},
		2: {Name: "series2", CreatedTimestamp: 1620000001},
	}

	createdTS := seriesSet.CreatedTS()
	require.Condition(t, func() bool {
		return createdTS == int64(1620000000) || createdTS == int64(1620000001)
	}, "CreatedTS() should return either 1620000000 or 1620000001")
}

func TestSeriesSet_LabelNames(t *testing.T) {
	t.Parallel()
	seriesSet := scrape.SeriesSet{
		1: {Name: "series1", Labels: labels.Labels{{Name: "label1"}, {Name: "label2"}}},
		2: {Name: "series2", Labels: labels.Labels{{Name: "label2"}, {Name: "label3"}}},
	}

	expected := "label1|label2|label3"
	actual := seriesSet.LabelNames()
	require.ElementsMatch(
		t,
		strings.Split(expected, "|"),
		strings.Split(actual, "|"),
		"LabelNames() should return the correct label names",
	)
}

func TestSeriesSet_LabelStats(t *testing.T) {
	t.Parallel()
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
	// Sort both slices by Name before comparison
	sort.Slice(expected, func(i, j int) bool { return expected[i].Name < expected[j].Name })
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	require.EqualValues(t, expected, got, "LabelStats() should return the correct label stats")
}

func TestSeriesSet_AsRowOrdering(t *testing.T) {
	t.Parallel()
	var seriesMap scrape.SeriesMap = make(map[string]scrape.SeriesSet)
	seriesMap["series1"] = scrape.SeriesSet{
		1: {Name: "series1", Labels: labels.Labels{{Name: "label1", Value: "foo"}}},
	}
	seriesMap["series2"] = scrape.SeriesSet{
		1: {Name: "series2", Labels: labels.Labels{{Name: "label1", Value: "foo"}}},
		2: {Name: "series2", Labels: labels.Labels{{Name: "label1", Value: "bar"}}},
	}
	seriesMap["series3"] = scrape.SeriesSet{
		1: {Name: "series3", Labels: labels.Labels{{Name: "label1", Value: "foo"}}},
		2: {Name: "series3", Labels: labels.Labels{{Name: "label1", Value: "bar"}}},
	}

	rows := seriesMap.AsRows()

	require.Len(t, rows, 3, "AsRows() should return the correct number of rows")
	require.Equal(t, "series2", rows[0].Name)
	require.Equal(t, "series3", rows[1].Name)
	require.Equal(t, "series1", rows[2].Name)
}
