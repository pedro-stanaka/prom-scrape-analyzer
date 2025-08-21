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
		1: {Name: "series1", Labels: labels.FromStrings("label1", "", "label2", "")},
		2: {Name: "series2", Labels: labels.FromStrings("label2", "", "label3", "")},
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
		1: {Name: "series1", Labels: labels.FromStrings("label1", "foo", "label2", "bar")},
		2: {Name: "series2", Labels: labels.FromStrings("label2", "baz", "label3", "qux")},
		3: {Name: "series3", Labels: labels.FromStrings("label2", "baz", "label3", "qua")},
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
		1: {Name: "series1", Labels: labels.FromStrings("label1", "foo")},
	}
	seriesMap["series2"] = scrape.SeriesSet{
		1: {Name: "series2", Labels: labels.FromStrings("label1", "foo")},
		2: {Name: "series2", Labels: labels.FromStrings("label1", "bar")},
	}
	seriesMap["series3"] = scrape.SeriesSet{
		1: {Name: "series3", Labels: labels.FromStrings("label1", "foo")},
		2: {Name: "series3", Labels: labels.FromStrings("label1", "bar")},
	}

	rows := seriesMap.AsRows()

	require.Len(t, rows, 3, "AsRows() should return the correct number of rows")
	require.Equal(t, "series2", rows[0].Name)
	require.Equal(t, "series3", rows[1].Name)
	require.Equal(t, "series1", rows[2].Name)
}

func TestSeries_Exemplars(t *testing.T) {
	t.Parallel()

	// Test series with exemplars
	series := scrape.Series{
		Name:   "test_metric",
		Labels: labels.FromStrings("method", "GET", "status", "200"),
		Type:   "counter",
		Exemplars: []scrape.Exemplar{
			{
				Labels: labels.FromStrings("trace_id", "abc123"),
				Value:  42.0,
				Ts:     1620000000000,
				HasTs:  true,
			},
			{
				Labels: labels.FromStrings("span_id", "xyz789"),
				Value:  100.5,
				Ts:     0,
				HasTs:  false,
			},
		},
	}

	require.Len(t, series.Exemplars, 2, "Series should have 2 exemplars")

	// Test first exemplar
	ex1 := series.Exemplars[0]
	require.Equal(t, 42.0, ex1.Value)
	require.Equal(t, int64(1620000000000), ex1.Ts)
	require.True(t, ex1.HasTs)
	require.Equal(t, "abc123", ex1.Labels.Get("trace_id"))

	// Test second exemplar
	ex2 := series.Exemplars[1]
	require.Equal(t, 100.5, ex2.Value)
	require.False(t, ex2.HasTs)
	require.Equal(t, "xyz789", ex2.Labels.Get("span_id"))

	// Test String() method
	ex1Str := ex1.String()
	require.Contains(t, ex1Str, "trace_id=\"abc123\"")
	require.Contains(t, ex1Str, "42")
	require.Contains(t, ex1Str, "2021-05-03") // Check timestamp is formatted

	ex2Str := ex2.String()
	require.Contains(t, ex2Str, "span_id=\"xyz789\"")
	require.Contains(t, ex2Str, "100.5")
	require.NotContains(t, ex2Str, "@") // No timestamp should be shown
}

func TestSeriesSet_WithExemplars(t *testing.T) {
	t.Parallel()

	seriesSet := scrape.SeriesSet{
		1: {
			Name:   "http_requests_total",
			Labels: labels.FromStrings("method", "GET"),
			Type:   "counter",
			Exemplars: []scrape.Exemplar{
				{
					Labels: labels.FromStrings("trace_id", "trace1"),
					Value:  123.0,
					Ts:     1620000000000,
					HasTs:  true,
				},
			},
		},
		2: {
			Name:   "http_requests_total",
			Labels: labels.FromStrings("method", "POST"),
			Type:   "counter",
			// No exemplars for this series
		},
	}

	// Check that we can access exemplars correctly
	series1 := seriesSet[1]
	require.Len(t, series1.Exemplars, 1)
	require.Equal(t, "trace1", series1.Exemplars[0].Labels.Get("trace_id"))

	series2 := seriesSet[2]
	require.Len(t, series2.Exemplars, 0)
}
