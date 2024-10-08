package main

import (
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/extkingpin"

	"github.com/pedro-stanaka/prom-scrape-analyzer/pkg/scrape"
)

type cardinalityOptions struct {
	Options
}

func (o *cardinalityOptions) addFlags(app extkingpin.AppClause) {
	o.AddFlags(app)
}

func registerCardinalityCommand(app *extkingpin.App) {
	cmd := app.Command("cardinality", "Analyze the cardinality of a Prometheus scrape job.")
	opts := &cardinalityOptions{}
	opts.addFlags(cmd)
	cmd.Setup(func(g *run.Group, logger log.Logger, reg *prometheus.Registry, _ opentracing.Tracer, _ <-chan struct{}, _ bool) error {
		// Dummy actor to immediately kill the group after the run function returns.
		g.Add(func() error { return nil }, func(error) {})

		scrapeURL := opts.ScrapeURL
		// TODO: enable passing this as a flag.
		timeoutDuration := 10 * time.Second

		level.Info(logger).Log("msg", "scraping", "url", scrapeURL, "timeout", timeoutDuration)
		scraper := scrape.NewPromScraper(scrapeURL, timeoutDuration, logger)
		metrics, err := scraper.Scrape()
		if err != nil {
			return err
		}

		for name, s := range metrics {
			// TODO: use something nice like https://github.com/charmbracelet/bubbles/?tab=readme-ov-file to display the data.
			level.Info(logger).Log("msg", "series", "name", name, "cardinality", s.Cardinality(), "type", s.MetricTypeString())
		}

		return nil
	})
}
