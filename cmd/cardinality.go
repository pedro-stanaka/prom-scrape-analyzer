package main

import (
	"strconv"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
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

var baseStyle = lipgloss.NewStyle().
	BorderStyle(lipgloss.NormalBorder()).
	BorderForeground(lipgloss.Color("240"))

type seriesTable struct {
	table     table.Model
	spinner   spinner.Model
	seriesMap scrape.SeriesMap

	loading   bool
	err       error
	infoTitle string
}

func newModel(sm map[string]scrape.SeriesSet, height int) *seriesTable {
	tbl := table.New(
		table.WithColumns([]table.Column{
			{Title: "Name", Width: 80},
			{Title: "Cardinality", Width: 16},
			{Title: "Type", Width: 10},
			{Title: "Labels", Width: 80},
			{Title: "Created TS", Width: 40},
		}),
		table.WithFocused(true),
		table.WithHeight(height),
	)

	tblStyle := table.DefaultStyles()
	tblStyle.Header = tblStyle.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	tblStyle.Selected = tblStyle.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	tbl.SetStyles(tblStyle)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m := &seriesTable{
		table:     tbl,
		seriesMap: sm,
		spinner:   sp,
		loading:   true,
	}

	return m
}

func (m *seriesTable) setData(sr *scrape.Result) {
	m.loading = false
	m.seriesMap = sr.Series
	m.infoTitle = m.formatInfoTitle(sr)

	var rows []table.Row
	for _, r := range m.seriesMap.AsRows() {
		rows = append(rows, table.Row{
			r.Name,
			strconv.Itoa(r.Cardinality),
			r.Type,
			r.Labels,
			r.CreatedTS,
		})
	}

	m.table.SetRows(rows)
}

func (m *seriesTable) View() string {
	if m.loading {
		return m.spinner.View() + "\nLoading..."
	}
	if m.err != nil {
		return baseStyle.Render("Exiting with error: " + m.err.Error())
	}

	return baseStyle.Render(m.table.View()) + "\n  " + m.table.HelpView() + "\n" + m.infoTitle
}

func (m *seriesTable) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *seriesTable) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "q", "ctrl+c":
			return m, tea.Quit
		case "down":
			if m.table.Cursor() < len(m.table.Rows())-1 {
				m.table, cmd = m.table.Update(msg)
			}
			return m, cmd
		case "up":
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		}
	case spinner.TickMsg:
		if m.loading {
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case error:
		m.loading = false
		m.err = msg
		return m, tea.Quit
	case *scrape.Result:
		m.setData(msg)
		return m, nil
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *seriesTable) formatInfoTitle(sr *scrape.Result) string {
	return "Scrape used content type: " + sr.UsedContentType
}

func registerCardinalityCommand(app *extkingpin.App) {
	cmd := app.Command("cardinality", "Analyze the cardinality of a Prometheus scrape job.")
	opts := &cardinalityOptions{}
	opts.addFlags(cmd)
	cmd.Setup(func(
		g *run.Group,
		logger log.Logger,
		reg *prometheus.Registry,
		_ opentracing.Tracer,
		_ <-chan struct{},
		_ bool,
	) error {
		scrapeURL := opts.ScrapeURL
		timeoutDuration := opts.Timeout

		metricTable := newModel(nil, opts.OutputHeight)
		p := tea.NewProgram(metricTable)

		// Create a channel to signal when scraping is complete
		scrapeDone := make(chan struct{})

		g.Add(func() error {
			_, err := p.Run()
			return err
		}, func(error) {
			close(scrapeDone)
		})

		g.Add(func() error {
			level.Info(logger).Log("msg", "scraping", "url", scrapeURL, "timeout", timeoutDuration)
			maxSize, err := opts.MaxScrapeSizeBytes()
			if err != nil {
				err = errors.Wrapf(err, "failed to parse max scrape size")
				p.Send(err)
				return err
			}
			scraper := scrape.NewPromScraper(
				scrapeURL,
				logger,
				scrape.WithTimeout(timeoutDuration),
				scrape.WithMaxBodySize(maxSize),
			)
			metrics, err := scraper.Scrape()
			if err != nil {
				p.Send(err)
				return err
			}

			// Send the scraped data to the UI
			level.Info(logger).Log("msg", "scraping complete")
			metricTable.setData(metrics)
			return nil
		}, func(error) {})

		return nil
	})
}
