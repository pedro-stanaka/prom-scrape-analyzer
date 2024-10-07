package main

import (
	"github.com/thanos-io/thanos/pkg/extkingpin"
)

type Options struct {
	ScrapeURL string
}

func (o *Options) AddFlags(app extkingpin.AppClause) {
	app.Flag("scrape-url", "The URL to scrape").Required().StringVar(&o.ScrapeURL)
}
