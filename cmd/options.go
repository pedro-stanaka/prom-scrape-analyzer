package main

import (
	"time"

	"github.com/thanos-io/thanos/pkg/extkingpin"
)

type Options struct {
	ScrapeURL     string
	OutputHeight  int
	MaxScrapeSize string
	Timeout       time.Duration
}

func (o *Options) AddFlags(app extkingpin.AppClause) {
	app.Flag("scrape-url", "The URL to scrape").Required().StringVar(&o.ScrapeURL)
	app.Flag("output-height", "The height of the output table").Default("15").IntVar(&o.OutputHeight)
	app.Flag("max-scrape-size", "The maximum size of the scrape").Default("50MiB").StringVar(&o.MaxScrapeSize)
	app.Flag("timeout", "The timeout for the scrape").Default("10s").DurationVar(&o.Timeout)
}
