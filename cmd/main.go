package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/thanos-io/thanos/pkg/extkingpin"
	"github.com/thanos-io/thanos/pkg/logging"
	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {
	app := extkingpin.NewApp(kingpin.New(filepath.Base(os.Args[0]), "A tool to analyze Prometheus scrape data."))
	logLevel := app.Flag("log.level", "Log filtering level.").
		Default("info").Enum("error", "warn", "info", "debug")
	logFormat := app.Flag("log.format", "Log format to use. Possible options: logfmt or json.").
		Default(logging.LogFormatLogfmt).Enum(logging.LogFormatLogfmt, logging.LogFormatJSON)
	logFile := app.Flag("log.file", "Log file to write to, if empty will log to stderr.").Default("").String()

	registerCardinalityCommand(app)

	cmd, setup := app.Parse()

	metrics := prometheus.NewRegistry()
	metrics.MustRegister(
		versioncollector.NewCollector("thanos"),
		collectors.NewGoCollector(
			collectors.WithGoCollectorRuntimeMetrics(collectors.GoRuntimeMetricsRule{Matcher: regexp.MustCompile("/.*")}),
		),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	logger, err := setupLogging(*logLevel, *logFormat, *logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up logging: %v\n", err)
		os.Exit(1)
	}

	// Create a signal channel to dispatch reload events to sub-commands.
	reloadCh := make(chan struct{}, 1)

	var g run.Group
	var tracer opentracing.Tracer
	if err := setup(&g, logger, metrics, tracer, reloadCh, *logLevel == "debug"); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		level.Error(logger).Log("err", fmt.Sprintf("%+v", errors.Wrapf(err, "preparing %s command failed", cmd)))
		os.Exit(1)
	}

	// Listen for termination signals.
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return interrupt(logger, cancel)
		}, func(error) {
			close(cancel)
		})
	}

	// Listen for reload signals.
	{
		cancel := make(chan struct{})
		g.Add(func() error {
			return reload(logger, cancel, reloadCh)
		}, func(error) {
			close(cancel)
		})
	}

	if err := g.Run(); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		level.Error(logger).Log("err", fmt.Sprintf("%+v", errors.Wrapf(err, "%s command failed", cmd)))
		os.Exit(1)
	}
	level.Info(logger).Log("msg", "exiting")
}

func setupLogging(logLevel, logFormat, file string) (log.Logger, error) {
	var (
		logger log.Logger
		lvl    level.Option
		writer io.Writer
	)

	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		// This enum is already checked and enforced by flag validations, so
		// this should never happen.
		panic("unexpected log level")
	}

	if file != "" {
		f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, err
		}
		writer = f
	} else {
		writer = os.Stderr
	}

	logger = log.NewLogfmtLogger(log.NewSyncWriter(writer))
	if logFormat == logging.LogFormatJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(writer))
	}

	// Sort the logger chain to avoid expensive log.Valuer evaluation for disallowed level.
	// Ref: https://github.com/go-kit/log/issues/14#issuecomment-945038252
	logger = log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.Caller(5))
	logger = level.NewFilter(logger, lvl)

	return logger, nil
}

func interrupt(logger log.Logger, cancel <-chan struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	select {
	case s := <-c:
		level.Info(logger).Log("msg", "caught signal. Exiting.", "signal", s)
		return nil
	case <-cancel:
		return errors.New("canceled")
	}
}

func reload(logger log.Logger, cancel <-chan struct{}, r chan<- struct{}) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGHUP)
	for {
		select {
		case s := <-c:
			level.Info(logger).Log("msg", "caught signal. Reloading.", "signal", s)
			select {
			case r <- struct{}{}:
				level.Info(logger).Log("msg", "reload dispatched.")
			default:
			}
		case <-cancel:
			return errors.New("canceled")
		}
	}
}
