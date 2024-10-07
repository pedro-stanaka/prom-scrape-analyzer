package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
	"github.com/thanos-io/thanos/pkg/extkingpin"
)

type cardinalityOptions struct {
	Options
}

func (o *cardinalityOptions) addFlags(app extkingpin.AppClause) {
	o.AddFlags(app)
}

type Series struct {
	Name            string
	Labels          labels.Labels
	Type            string
	HasCTZeroSample bool
}

type SeriesSet map[uint64]Series

func registerCardinalityCommand(app *extkingpin.App) {
	cmd := app.Command("cardinality", "Analyze the cardinality of a Prometheus scrape job.")
	opts := &cardinalityOptions{}
	opts.addFlags(cmd)
	cmd.Setup(func(g *run.Group, logger log.Logger, reg *prometheus.Registry, _ opentracing.Tracer, _ <-chan struct{}, _ bool) error {
		// Read the scrape URL from the command line.
		scrapeURL := opts.ScrapeURL
		// Scrape the URL and analyze the cardinality.
		req, err := http.NewRequest("GET", scrapeURL, nil)
		if err != nil {
			return err
		}

		// TODO: handle OpenMetrics format as well, we should retry if the client does not support the format.
		acceptHeader := acceptHeader([]config.ScrapeProtocol{
			config.PrometheusProto,
			config.OpenMetricsText1_0_0,
			config.PrometheusText0_0_4,
			config.OpenMetricsText0_0_1,
		})
		req.Header.Set("Accept", acceptHeader)
		req.Header.Set("Accept-Encoding", "gzip")
		// TODO: enable passing this as a flag.
		req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", "20")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Slurp the response body.
		contentType, body, err := readResponse(resp, logger)
		if err != nil {
			return err
		}

		metrics, err := extractMetrics(body, contentType, logger)
		if err != nil {
			return err
		}

		for name, s := range metrics {
			// TODO: series could have mixed types, we should handle that.
			// TODO: use something nice like https://github.com/charmbracelet/bubbles/?tab=readme-ov-file to display the data.
			level.Info(logger).Log("msg", "series", "name", name, "count", len(s), "type", s[0].Type)
		}

		return nil
	})
}

func readResponse(resp *http.Response, logger log.Logger) (string, []byte, error) {
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}

	// TODO: allow for a configurable limit.
	bodySizeLimit := int64(10 * 1024 * 1024) // 10MB limit, adjust as needed
	var reader io.Reader = resp.Body

	if resp.Header.Get("Content-Encoding") == "gzip" {
		var err error
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return "", nil, err
		}
		defer reader.(*gzip.Reader).Close()
	}

	body, err := io.ReadAll(io.LimitReader(reader, bodySizeLimit))
	if err != nil {
		return "", nil, err
	}

	if int64(len(body)) >= bodySizeLimit {
		level.Warn(logger).Log("msg", "response body size limit exceeded")
		return "", nil, fmt.Errorf("response body size limit exceeded")
	}

	return resp.Header.Get("Content-Type"), body, nil
}

func extractMetrics(body []byte, contentType string, logger log.Logger) (map[string]SeriesSet, error) {
	metrics := make(map[string]SeriesSet)
	// Parse the response body.
	parser, err := textparse.New(body, contentType, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	var (
		lset        labels.Labels
		currentType string
		val         float64
		t           int64
	)

	// Analyze the cardinality.
	for {
		entry, err := parser.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			level.Debug(logger).Log("msg", "failed to parse entry", "err", err)
			continue
		}

		switch entry {
		case textparse.EntryType:
			// Handle metric type
			_, metricType := parser.Type()
			currentType = string(metricType)
			level.Debug(logger).Log("msg", "found metric type", "type", metricType)

		case textparse.EntryHelp:
			// Handle metric help text
			metricName, help := parser.Help()
			level.Debug(logger).Log("msg", "found metric help", "metric", string(metricName), "help", help)

		case textparse.EntryUnit:
			// Handle metric unit
			metricName, unit := parser.Unit()
			level.Debug(logger).Log("msg", "found metric unit", "metric", string(metricName), "unit", unit)

		case textparse.EntrySeries:
			// Handle series data
			_ = parser.Metric(&lset)

			metricName := lset.Get(labels.MetricName)
			if metricName == "" {
				level.Debug(logger).Log("msg", "metric name not found in labels", "labels", lset.String())
				continue
			}

			if _, ok := metrics[metricName]; !ok {
				metrics[metricName] = make(SeriesSet)
			}

			hash := lset.Hash()
			series := Series{
				Name:            metricName,
				Labels:          lset.Copy(),
				Type:            currentType,
				HasCTZeroSample: true,
			}

			metrics[metricName][hash] = series

			level.Debug(logger).Log("msg", "found series", "metric", metricName, "labels", lset.String(), "value", val, "timestamp", t, "has_ct_zero", series.HasCTZeroSample)

		case textparse.EntryHistogram:
			// Handle histogram data
			_, t, h, fh := parser.Histogram()
			_ = parser.Metric(&lset)

			metricName := lset.Get(labels.MetricName)
			if metricName == "" {
				level.Debug(logger).Log("msg", "histogram metric name not found in labels", "labels", lset.String())
				continue
			}

			if _, ok := metrics[metricName]; !ok {
				metrics[metricName] = make(SeriesSet)
			}

			hash := lset.Hash()
			series := Series{
				Name:   metricName,
				Labels: lset.Copy(),
				Type:   "native_histogram",
			}

			metrics[metricName][hash] = series

			if h != nil {
				level.Debug(logger).Log("msg", "found histogram", "metric", metricName, "labels", lset.String(), "timestamp", t, "has_ct_zero", series.HasCTZeroSample)
			} else if fh != nil {
				level.Debug(logger).Log("msg", "found float histogram", "metric", metricName, "labels", lset.String(), "timestamp", t, "has_ct_zero", series.HasCTZeroSample)
			}

		default:
			level.Debug(logger).Log("msg", "unknown entry type", "type", entry)
		}
	}

	return metrics, nil
}

// acceptHeader transforms preference from the options into specific header values as
// https://www.rfc-editor.org/rfc/rfc9110.html#name-accept defines.
// No validation is here, we expect scrape protocols to be validated already.
func acceptHeader(sps []config.ScrapeProtocol) string {
	var vals []string
	weight := len(config.ScrapeProtocolsHeaders) + 1
	for _, sp := range sps {
		vals = append(vals, fmt.Sprintf("%s;q=0.%d", config.ScrapeProtocolsHeaders[sp], weight))
		weight--
	}
	// Default match anything.
	vals = append(vals, fmt.Sprintf("*/*;q=0.%d", weight))
	return strings.Join(vals, ",")
}
