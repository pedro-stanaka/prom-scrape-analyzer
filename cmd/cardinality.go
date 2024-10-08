package main

import (
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

type model struct {
	table     table.Model
	seriesMap scrape.SeriesMap
}

func newModel(sm map[string]scrape.SeriesSet) *model {
	tbl := table.New(
		table.WithColumns([]table.Column{
			{Title: "Name", Width: 80},
			{Title: "Cardinality", Width: 20},
			{Title: "Type", Width: 20},
			{Title: "Labels", Width: 80},
			{Title: "Created TS", Width: 80},
		}),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	tbl.SetStyles(s)

	return &model{
		table:     tbl,
		seriesMap: sm,
	}
}

func (m *model) updateRows() {
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

func (m *model) View() string {
	m.updateRows()
	return m.table.View()
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "enter":
			return m, tea.Batch(
				tea.Printf("Let's go to %s!", m.table.SelectedRow()[1]),
			)
		case "down":
			if m.table.Cursor() < len(m.table.Rows())-1 {
				m.table, cmd = m.table.Update(msg)
			}
			return m, cmd
		case "up":
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		}
	}
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func registerCardinalityCommand(app *extkingpin.App) {
	cmd := app.Command("cardinality", "Analyze the cardinality of a Prometheus scrape job.")
	opts := &cardinalityOptions{}
	opts.addFlags(cmd)
	cmd.Setup(func(g *run.Group, logger log.Logger, reg *prometheus.Registry, _ opentracing.Tracer, _ <-chan struct{}, _ bool) error {
		scrapeURL := opts.ScrapeURL
		// TODO: enable passing this as a flag.
		timeoutDuration := 10 * time.Second

		level.Info(logger).Log("msg", "scraping", "url", scrapeURL, "timeout", timeoutDuration)
		scraper := scrape.NewPromScraper(scrapeURL, timeoutDuration, logger)
		metrics, err := scraper.Scrape()
		if err != nil {
			return err
		}

		g.Add(func() error {
			metricTable := newModel(metrics)
			p := tea.NewProgram(metricTable)

			_, err := p.Run()
			return err
		}, func(error) {})

		return nil
	})
}
