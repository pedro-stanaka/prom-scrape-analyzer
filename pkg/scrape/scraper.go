package scrape

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	config_util "github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
	"github.com/prometheus/prometheus/model/timestamp"
)

type PromScraper struct {
	httpConfigFile        string
	scrapeURL             string
	scrapeFilePath        string
	timeout               time.Duration
	logger                log.Logger
	series                map[string]SeriesSet
	lastScrapeContentType string
	maxBodySize           int64
}

type scrapeOpts struct {
	httpConfigFile string
	timeout        time.Duration
	maxBodySize    int64
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

func WithHttpConfigFile(file string) ScraperOption {
	return func(opts *scrapeOpts) {
		opts.httpConfigFile = file
	}
}

func NewPromScraper(scrapeURL string, scrapeFile string, logger log.Logger, opts ...ScraperOption) *PromScraper {
	scOpts := &scrapeOpts{
		timeout:        10 * time.Second,
		maxBodySize:    10 * 1024 * 1024,
		httpConfigFile: "",
	}

	for _, opt := range opts {
		opt(scOpts)
	}

	return &PromScraper{
		scrapeURL:      scrapeURL,
		scrapeFilePath: scrapeFile,
		logger:         logger,
		timeout:        scOpts.timeout,
		maxBodySize:    scOpts.maxBodySize,
		httpConfigFile: scOpts.httpConfigFile,

		series: make(map[string]SeriesSet),
	}
}

func (ps *PromScraper) Scrape() (*Result, error) {
	if ps.scrapeFilePath != "" {
		return ps.scrapeFile()
	}

	return ps.scrapeHTTP()
}

func (ps *PromScraper) scrapeFile() (*Result, error) {
	var (
		seriesSet        map[string]SeriesSet
		seriesScrapeText SeriesScrapeText
	)

	// Don't use os.ReadFile(); manually open the file so we can create an
	// io.LimitReader from the file to enforce max body size.
	f, err := os.Open(ps.scrapeFilePath)
	if err != nil {
		return &Result{}, fmt.Errorf("failed to open file %s to scrape metrics: %w", ps.scrapeFilePath, err)
	}
	defer f.Close()

	body, err := io.ReadAll(io.LimitReader(f, ps.maxBodySize))
	if err != nil {
		return &Result{}, fmt.Errorf("failed reading file %s to scrape metrics: %w", ps.scrapeFilePath, err)
	}

	if int64(len(body)) >= ps.maxBodySize {
		level.Warn(ps.logger).Log(
			"msg", "metric file body size limit exceeded",
			"limit_bytes", ps.maxBodySize,
			"body_size", len(body),
		)
		return &Result{}, fmt.Errorf("metric file body size exceeded limit of %d bytes", ps.maxBodySize)
	}

	// assume that scraping metrics from a file implies they're in text format.
	contentType := "text/plain"
	ps.lastScrapeContentType = contentType
	seriesSet, scrapeErr := ps.extractMetrics(body, contentType)
	if scrapeErr != nil {
		return &Result{}, fmt.Errorf("failed to extract metrics from file: %w", scrapeErr)
	}
	seriesScrapeText = ps.extractMetricSeriesText(body)

	return &Result{
		Series:           seriesSet,
		UsedContentType:  contentType,
		SeriesScrapeText: seriesScrapeText,
	}, nil
}

func (ps *PromScraper) scrapeHTTP() (*Result, error) {
	var (
		seriesSet        map[string]SeriesSet
		scrapeErr        error
		seriesScrapeText SeriesScrapeText
		textScrapeErr    error
		wg               sync.WaitGroup
	)

	httpClient := http.DefaultClient
	if ps.httpConfigFile != "" {
		httpCfg, _, err := config_util.LoadHTTPConfigFile(ps.httpConfigFile)
		if err != nil {
			return &Result{}, fmt.Errorf("failed to load HTTP configuration file %s: %w", ps.httpConfigFile, err)
		}

		if err = httpCfg.Validate(); err != nil {
			return &Result{}, fmt.Errorf("failed to validate HTTP configuration file %s: %w", ps.httpConfigFile, err)
		}

		httpClient, err = config_util.NewClientFromConfig(*httpCfg, "prom-scrape-analyzer")
		if err != nil {
			return &Result{}, fmt.Errorf("failed to create HTTP client from configuration file %s: %w", ps.httpConfigFile, err)
		}
	}

	// First prioritize scraping PrometheusProto format for access to data about created timestamps and native histograms
	wg.Add(1)
	go func() {
		defer wg.Done()

		req, err := ps.setupRequest([]config.ScrapeProtocol{
			config.PrometheusProto,
			config.OpenMetricsText1_0_0,
			config.PrometheusText0_0_4,
			config.OpenMetricsText0_0_1,
		})
		if err != nil {
			return
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			scrapeErr = err
			return
		}
		defer resp.Body.Close()

		contentType, body, err := ps.readResponse(resp)
		if err != nil {
			scrapeErr = err
			return
		}
		ps.lastScrapeContentType = contentType

		seriesSet, scrapeErr = ps.extractMetrics(body, contentType)
	}()

	// If the above response is in proto format then it isn't in a human-readable format,
	// so request a format known to be readable in case the user wants to view the series.
	wg.Add(1)
	go func() {
		defer wg.Done()

		textReq, err := ps.setupRequest([]config.ScrapeProtocol{
			config.OpenMetricsText1_0_0,
			config.PrometheusText0_0_4,
			config.OpenMetricsText0_0_1,
		})
		if err != nil {
			textScrapeErr = err
			return
		}

		textResp, err := httpClient.Do(textReq)
		if err != nil {
			textScrapeErr = err
			return
		}
		defer textResp.Body.Close()
		_, textBody, err := ps.readResponse(textResp)
		if err != nil {
			textScrapeErr = err
			return
		}

		seriesScrapeText = ps.extractMetricSeriesText(textBody)
	}()

	wg.Wait()

	if scrapeErr != nil {
		return nil, scrapeErr
	}
	if textScrapeErr != nil {
		return nil, textScrapeErr
	}

	return &Result{
		Series:           seriesSet,
		UsedContentType:  ps.lastScrapeContentType,
		SeriesScrapeText: seriesScrapeText,
	}, nil
}

func (ps *PromScraper) LastScrapeContentType() string {
	return ps.lastScrapeContentType
}

func (ps *PromScraper) setupRequest(accept []config.ScrapeProtocol) (*http.Request, error) {
	// Scrape the URL and analyze the cardinality.
	req, err := http.NewRequest("GET", ps.scrapeURL, nil)
	if err != nil {
		return nil, err
	}

	acceptHeader := acceptHeader(accept)
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
	parser, err := textparse.New(body, contentType, "", false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	var (
		lset           labels.Labels
		currentType    string
		baseMetricName string
		defTime        = timestamp.FromTime(time.Now())
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
			metricName, metricType := parser.Type()
			currentType = string(metricType)
			baseMetricName = string(metricName)

			continue // Skip to next iteration as we don't need to process this entry further

		case textparse.EntrySeries:
			parser.Labels(&lset)
			metricName := lset.Get(labels.MetricName)
			if metricName == "" {
				level.Debug(ps.logger).Log("msg", "metric name not found in labels", "labels", lset.String())
				continue
			}

			// Combine series belonging to the same classic histogram or summary metric
			// ex: the series elapsed_seconds_bucket, elapsed_seconds_count, elapsed_seconds_sum are tracked as elapsed_seconds
			if currentType == "histogram" || currentType == "summary" {
				metricName = baseMetricName
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
			if ctMs != 0 {
				series.CreatedTimestamp = ctMs
				level.Debug(ps.logger).Log("msg", "found CT zero sample", "metric", metricName, "ct", ctMs)
			}

			// Collect exemplars for this series
			var exemplars []Exemplar
			ex := &exemplar.Exemplar{}
			for parser.Exemplar(ex) {
				exemplars = append(exemplars, Exemplar{
					Labels: ex.Labels.Copy(),
					Value:  ex.Value,
					Ts:     ex.Ts,
					HasTs:  ex.HasTs,
				})
				ex = &exemplar.Exemplar{}
			}
			series.Exemplars = exemplars

			metrics[metricName][hash] = series

			level.Debug(ps.logger).Log(
				"msg", "found series",
				"metric", metricName,
				"labels", lset.String(),
				"type", currentType,
				"timestamp", t,
				"has_ct_zero", series.CreatedTimestamp != 0,
				"exemplar_count", len(exemplars),
			)
		case textparse.EntryHistogram:
			// Processing solely for native histograms
			parser.Labels(&lset)
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
			if ctMs != 0 {
				series.CreatedTimestamp = ctMs
				level.Debug(ps.logger).Log(
					"msg", "found CT zero sample for histogram",
					"metric", metricName,
					"ct", ctMs,
				)
			}

			// Collect exemplars for this histogram
			var exemplars []Exemplar
			ex := &exemplar.Exemplar{}
			for parser.Exemplar(ex) {
				exemplars = append(exemplars, Exemplar{
					Labels: ex.Labels.Copy(),
					Value:  ex.Value,
					Ts:     ex.Ts,
					HasTs:  ex.HasTs,
				})
				ex = &exemplar.Exemplar{}
			}
			series.Exemplars = exemplars

			metrics[metricName][hash] = series

			if h != nil {
				level.Debug(ps.logger).Log(
					"msg", "found histogram",
					"metric", metricName,
					"labels", lset.String(),
					"type", "histogram",
					"timestamp", t,
					"has_ct_zero", series.CreatedTimestamp != 0,
					"exemplar_count", len(exemplars),
				)
			} else if fh != nil {
				level.Debug(ps.logger).Log(
					"msg", "found float histogram",
					"metric", metricName,
					"labels", lset.String(),
					"type", "float_histogram",
					"timestamp", t,
					"has_ct_zero", series.CreatedTimestamp != 0,
					"exemplar_count", len(exemplars),
				)
			}

		default:
			level.Debug(ps.logger).Log("msg", "unknown entry type", "type", entry)
		}
	}

	return metrics, nil
}

func (ps *PromScraper) extractMetricSeriesText(textScrapeResponse []byte) SeriesScrapeText {
	seriesScrapeText := make(map[string]string)
	metricNamePattern := regexp.MustCompile(`^[^{\s]+`)
	lines := strings.Split(string(textScrapeResponse), "\n")
	// a metric's series are not on consecutive lines for histogram and summary metrics
	// so a strings.Builder is kept in memory for each metric
	metricLines := make(map[string]*strings.Builder)

	// For histograms and summaries, we need to map the base metric name to all its suffixes
	baseMetrics := make(map[string]bool)

	// First pass: identify histogram and summary base metrics
	for _, line := range lines {
		if strings.HasPrefix(line, "# TYPE") {
			parts := strings.Fields(line)
			if len(parts) >= 4 && (parts[3] == "histogram" || parts[3] == "summary") {
				baseMetrics[parts[2]] = true
			}
		}
	}

	// Second pass: collect all lines
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		var parsedMetric string
		if strings.HasPrefix(line, "#") {
			parts := strings.Split(line, " ")
			if len(parts) >= 3 {
				parsedMetric = parts[2]
			}
		} else {
			parsedMetric = metricNamePattern.FindString(line)
		}
		if parsedMetric == "" {
			_ = level.Debug(ps.logger).Log("msg", "failed to parse metric name from line", "line", line)
			continue
		}

		// For histogram and summary metrics, also add the line to the base metric
		baseMetric := parsedMetric
		for prefix := range baseMetrics {
			if strings.HasPrefix(parsedMetric, prefix+"_") {
				baseMetric = prefix
				break
			}
		}

		sb, ok := metricLines[baseMetric]
		if !ok {
			sb = &strings.Builder{}
			metricLines[baseMetric] = sb
		}

		sb.WriteString(line)
		sb.WriteString("\n")
	}

	for metric, sb := range metricLines {
		seriesScrapeText[metric] = sb.String()
	}
	return seriesScrapeText
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
