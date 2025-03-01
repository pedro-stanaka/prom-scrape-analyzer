package main

import (
	"time"

	"github.com/docker/go-units"
	"github.com/pkg/errors"
	"github.com/thanos-io/thanos/pkg/extkingpin"
)

type Options struct {
	ScrapeURL      string
	ScrapeFile     string
	OutputHeight   int
	MaxScrapeSize  string
	Timeout        time.Duration
	HttpConfigFile string
}

func (o *Options) MaxScrapeSizeBytes() (int64, error) {
	size, err := units.FromHumanSize(o.MaxScrapeSize)
	if err != nil {
		return 0, errors.Wrap(err, "invalid max scrape size")
	}
	return size, nil
}

func (o *Options) AddFlags(app extkingpin.AppClause) {
	app.Flag("scrape.url", "URL to scrape metrics from").
		Default("").
		StringVar(&o.ScrapeURL)

	app.Flag("scrape.file", "File to scrape metrics from").
		Default("").
		StringVar(&o.ScrapeFile)

	app.Flag("timeout", "Timeout for the scrape request").
		Default("10s").
		DurationVar(&o.Timeout)

	app.Flag("output-height", "Height of the output table").
		Default("40").
		IntVar(&o.OutputHeight)

	app.Flag("max-scrape-size", "Maximum size of the scrape response body (e.g. 10MB, 1GB)").
		Default("100MB").
		StringVar(&o.MaxScrapeSize)

	app.Flag("http.config", "Path to file to use for HTTP client config options like basic auth and TLS.").
		Default("").
		StringVar(&o.HttpConfigFile)
}
