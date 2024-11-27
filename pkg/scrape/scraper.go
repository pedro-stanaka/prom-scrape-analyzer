package scrape

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
	"github.com/prometheus/prometheus/model/timestamp"
)

type PromScraper struct {
	scrapeURL             string
	timeout               time.Duration
	logger                log.Logger
	series                map[string]SeriesSet
	lastScrapeContentType string
	maxBodySize           int64
}

type scrapeOpts struct {
	timeout     time.Duration
	maxBodySize int64
}

type ScraperOption func(*scrapeOpts)

func WithTimeout(timeout time.Duration) ScraperOption {
	return func(opts *scrapeOpts) {
		opts.timeout = timeout
	}
}

func WithMaxBodySize(maxBodySize int64) ScraperOption {
	return func(opts *scrapeOpts) {
		opts.maxBodySize = maxBodySize
	}
}

func NewPromScraper(scrapeURL string, logger log.Logger, opts ...ScraperOption) *PromScraper {
	scOpts := &scrapeOpts{
		timeout:     10 * time.Second,
		maxBodySize: 10 * 1024 * 1024,
	}

	for _, opt := range opts {
		opt(scOpts)
	}

	return &PromScraper{
		scrapeURL:   scrapeURL,
		logger:      logger,
		timeout:     scOpts.timeout,
		maxBodySize: scOpts.maxBodySize,

		series: make(map[string]SeriesSet),
	}
}

func (ps *PromScraper) Scrape() (*Result, error) {
	req, err := ps.setupRequest()
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType, body, err := ps.readResponse(resp)
	if err != nil {
		return nil, err
	}

	ps.lastScrapeContentType = contentType

	metrics, err := ps.extractMetrics(body, contentType)
	if err != nil {
		return nil, err
	}

	return &Result{
		Series:          metrics,
		UsedContentType: contentType,
	}, nil
}

func (ps *PromScraper) LastScrapeContentType() string {
	return ps.lastScrapeContentType
}

func (ps *PromScraper) setupRequest() (*http.Request, error) {
	// Scrape the URL and analyze the cardinality.
	req, err := http.NewRequest("GET", ps.scrapeURL, nil)
	if err != nil {
		return nil, err
	}

	acceptHeader := acceptHeader([]config.ScrapeProtocol{
		config.PrometheusProto,
		config.OpenMetricsText1_0_0,
		config.PrometheusText0_0_4,
		config.OpenMetricsText0_0_1,
	})
	req.Header.Set("Accept", acceptHeader)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", strconv.FormatInt(int64(ps.timeout.Seconds()), 10))
	return req, nil
}

func (ps *PromScraper) readResponse(resp *http.Response) (string, []byte, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}

	var reader io.Reader = resp.Body

	if resp.Header.Get("Content-Encoding") == "gzip" {
		var err error
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return "", nil, err
		}
		defer reader.(*gzip.Reader).Close()
	}

	body, err := io.ReadAll(io.LimitReader(reader, ps.maxBodySize))
	if err != nil {
		return "", nil, err
	}

	if int64(len(body)) >= ps.maxBodySize {
		level.Warn(ps.logger).Log(
			"msg", "response body size limit exceeded",
			"limit_bytes", ps.maxBodySize,
			"body_size", len(body),
		)
		return "", nil, fmt.Errorf("response body size exceeded limit of %d bytes", ps.maxBodySize)
	}

	return resp.Header.Get("Content-Type"), body, nil
}

func (ps *PromScraper) extractMetrics(body []byte, contentType string) (map[string]SeriesSet, error) {
	metrics := make(map[string]SeriesSet)
	parser, err := textparse.New(body, contentType, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	var (
		lset        labels.Labels
		currentType string
		defTime     = timestamp.FromTime(time.Now())
	)

	for {
		entry, err := parser.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			level.Debug(ps.logger).Log("msg", "failed to parse entry", "err", err)
			continue
		}

		switch entry {
		case textparse.EntryType:
			_, metricType := parser.Type()
			currentType = string(metricType)
			continue // Skip to next iteration as we don't need to process this entry further

		case textparse.EntrySeries:
			_ = parser.Metric(&lset)
			metricName := lset.Get(labels.MetricName)
			if metricName == "" {
				level.Debug(ps.logger).Log("msg", "metric name not found in labels", "labels", lset.String())
				continue
			}

			if _, ok := metrics[metricName]; !ok {
				metrics[metricName] = make(SeriesSet)
			}

			hash := lset.Hash()
			series := Series{
				Name:   metricName,
				Labels: lset.Copy(),
				Type:   currentType, // clone type string
			}

			_, ts, _ := parser.Series()
			t := defTime
			if ts != nil {
				t = *ts
			}

			ctMs := parser.CreatedTimestamp()
			if ctMs != nil {
				series.CreatedTimestamp = *ctMs
				level.Debug(ps.logger).Log("msg", "found CT zero sample", "metric", metricName, "ct", *ctMs)
			}

			metrics[metricName][hash] = series

			level.Debug(ps.logger).Log(
				"msg", "found series",
				"metric", metricName,
				"labels", lset.String(),
				"type", currentType,
				"timestamp", t,
				"has_ct_zero", series.CreatedTimestamp != 0,
			)

		case textparse.EntryHistogram:
			_ = parser.Metric(&lset)
			metricName := lset.Get(labels.MetricName)
			if metricName == "" {
				level.Debug(ps.logger).Log("msg", "histogram metric name not found in labels", "labels", lset.String())
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

			_, ts, h, fh := parser.Histogram()
			t := defTime
			if ts != nil {
				t = *ts
			}

			ctMs := parser.CreatedTimestamp()
			if ctMs != nil {
				series.CreatedTimestamp = *ctMs
				level.Debug(ps.logger).Log(
					"msg", "found CT zero sample for histogram",
					"metric", metricName,
					"ct", *ctMs,
				)
			}

			metrics[metricName][hash] = series

			if h != nil {
				level.Debug(ps.logger).Log(
					"msg", "found histogram",
					"metric", metricName,
					"labels", lset.String(),
					"type", "histogram",
					"timestamp", t,
					"has_ct_zero", series.CreatedTimestamp != 0,
				)
			} else if fh != nil {
				level.Debug(ps.logger).Log(
					"msg", "found float histogram",
					"metric", metricName,
					"labels", lset.String(),
					"type", "float_histogram",
					"timestamp", t,
					"has_ct_zero", series.CreatedTimestamp != 0,
				)
			}

		default:
			level.Debug(ps.logger).Log("msg", "unknown entry type", "type", entry)
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
