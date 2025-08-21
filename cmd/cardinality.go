package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/oklog/run"
	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/thanos-io/thanos/pkg/extkingpin"

	"github.com/pedro-stanaka/prom-scrape-analyzer/internal"
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

var tableHelp = help.New().ShortHelpView([]key.Binding{
	key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	key.NewBinding(
		key.WithKeys("m"),
		key.WithHelp("m", "search metrics"),
	),
	key.NewBinding(
		key.WithKeys("l"),
		key.WithHelp("l", "search label names"),
	),
	key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "search metric and label names"),
	),
	key.NewBinding(
		key.WithKeys("enter", "v"),
		key.WithHelp("v/↵", "view series in text editor"),
	),
	key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "view exemplars"),
	),
})
var searchHelp = help.New().ShortHelpView([]key.Binding{
	key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("↵", "explore table"),
	),
	key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc:", "clear search"),
	),
})

var noFiltering func(info scrape.SeriesInfo) bool = nil

var flashDuration = 5 * time.Second

type searchType int

const (
	searchNone searchType = iota
	searchMetrics
	searchLabels
	searchAll
)

type seriesTable struct {
	table            table.Model
	spinner          spinner.Model
	searchInput      textinput.Model
	seriesMap        scrape.SeriesMap
	seriesScrapeText scrape.SeriesScrapeText
	loading          bool
	search           searchType
	err              error
	infoTitle        string
	flashMsg         internal.TextFlash
	program          *tea.Program
	logger           log.Logger
}

func newModel(sm map[string]scrape.SeriesSet, height int, logger log.Logger) *seriesTable {
	tbl := table.New(
		table.WithColumns([]table.Column{
			{Title: "Name", Width: 60},
			{Title: "Cardinality", Width: 16},
			{Title: "Type", Width: 10},
			{Title: "Labels", Width: 80},
			{Title: "Created TS", Width: 50},
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

	ti := textinput.New()
	ti.Placeholder = "Search value"

	m := &seriesTable{
		table:       tbl,
		seriesMap:   sm,
		spinner:     sp,
		searchInput: ti,
		loading:     true,
		search:      searchNone,
		flashMsg:    internal.TextFlash{},
		logger:      logger,
	}

	return m
}

func (m *seriesTable) setTableRows(filter func(info scrape.SeriesInfo) bool) {
	var rows []table.Row
	for _, r := range m.seriesMap.AsRows() {
		if filter == nil || filter(r) {
			rows = append(rows, table.Row{
				r.Name,
				strconv.Itoa(r.Cardinality),
				r.Type,
				r.Labels,
				r.CreatedTS,
			})
		}
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

	var view strings.Builder

	if m.search != searchNone {
		view.WriteString(baseStyle.Render(m.searchInput.View()))
	}

	flashText := m.flashMsg.View()
	if flashText != "" {
		if m.search != searchNone {
			view.WriteString("\n")
		}
		view.WriteString(flashText)
	}

	view.WriteString("\n")
	view.WriteString(baseStyle.Render(m.table.View()))

	view.WriteString("\n")
	if m.searchInput.Focused() {
		view.WriteString(searchHelp)
	} else {
		view.WriteString(tableHelp)
	}

	if m.search != searchNone {
		total := len(m.seriesMap)
		filtered := len(m.table.Rows())
		view.WriteString("\n")
		view.WriteString(fmt.Sprintf("Showing %d out of %d metrics", filtered, total))
	} else {
		total := len(m.seriesMap)
		view.WriteString("\n")
		view.WriteString(fmt.Sprintf("Total metrics: %d", total))
		view.WriteString("\n")
		view.WriteString(m.infoTitle)
	}

	return view.String()
}

func (m *seriesTable) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *seriesTable) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		}
	case spinner.TickMsg:
		if m.loading {
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
	case internal.UpdateTextFlashMsg:
		m.flashMsg, cmd = m.flashMsg.Update(msg)
		return m, cmd
	case error:
		m.loading = false
		m.err = msg
		return m, tea.Quit
	case *scrape.Result:
		m.loading = false
		m.seriesMap = msg.Series
		m.seriesScrapeText = msg.SeriesScrapeText
		m.infoTitle = m.formatInfoTitle(msg)
		m.setTableRows(noFiltering)
		return m, nil
	}

	if m.search != searchNone {
		return m.updateWhileSearchingMetrics(msg)
	} else {
		return m.updateWhileBrowsingTable(msg)
	}
}

func (m *seriesTable) updateWhileBrowsingTable(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "esc":
			if m.table.Focused() {
				m.table.Blur()
			} else {
				m.table.Focus()
			}
		case "down":
			if m.table.Cursor() < len(m.table.Rows())-1 {
				m.table, cmd = m.table.Update(msg)
			}
			return m, cmd
		case "up":
			m.table, cmd = m.table.Update(msg)
			return m, cmd
		case "e":
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, m.flashMsg.Flash("No series available to view exemplars", internal.Error, flashDuration)
			}

			metricName := selectedRow[0]
			seriesSet, exists := m.seriesMap[metricName]
			if !exists {
				return m, m.flashMsg.Flash("Metric not found", internal.Error, flashDuration)
			}

			// Collect all exemplars from all series of this metric
			var exemplarText strings.Builder
			exemplarText.WriteString(fmt.Sprintf("# Exemplars for metric: %s\n\n", metricName))

			hasExemplars := false
			for _, series := range seriesSet {
				if len(series.Exemplars) > 0 {
					hasExemplars = true
					exemplarText.WriteString(fmt.Sprintf("## Series: %s\n", series.Labels.String()))
					for i, ex := range series.Exemplars {
						exemplarText.WriteString(fmt.Sprintf("  [%d] %s\n", i+1, ex.String()))
					}
					exemplarText.WriteString("\n")
				}
			}

			if !hasExemplars {
				return m, m.flashMsg.Flash("No exemplars found for this metric", internal.Info, flashDuration)
			}

			// Create temp file and open in editor
			tmpFile := internal.CreateTempFileWithContent(exemplarText.String())
			if tmpFile == "" {
				return m, m.flashMsg.Flash("Failed to create temporary file", internal.Error, flashDuration)
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				os.Remove(tmpFile)
				return m, m.flashMsg.Flash("Please set the EDITOR environment variable", internal.Error, flashDuration)
			}

			// Run the editor
			cmd := exec.Command(editor, tmpFile)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			err := m.program.ReleaseTerminal()
			if err != nil {
				return m, m.flashMsg.Flash("Error preparing to view exemplars: "+err.Error(), internal.Error, flashDuration)
			}

			err = cmd.Run()

			// Restore terminal after editor closes
			restoreErr := m.program.RestoreTerminal()
			if restoreErr != nil {
				_ = level.Warn(m.logger).Log("msg", "Failed to restore terminal", "err", restoreErr)
			}

			if err != nil {
				return m, m.flashMsg.Flash("Failed to run editor: "+err.Error(), internal.Error, flashDuration)
			}
			return m, nil
		case "enter", "v":
			selectedRow := m.table.SelectedRow()
			if len(selectedRow) == 0 {
				return m, m.flashMsg.Flash("No series available to open", internal.Error, flashDuration)
			}

			metricName := selectedRow[0]
			seriesText := m.seriesScrapeText[metricName]

			tmpFile := internal.CreateTempFileWithContent(seriesText)
			if tmpFile == "" {
				return m, m.flashMsg.Flash("Failed to create temporary file", internal.Error, flashDuration)
			}

			editor := os.Getenv("EDITOR")
			if editor == "" {
				os.Remove(tmpFile)
				return m, m.flashMsg.Flash("Please set the EDITOR environment variable", internal.Error, flashDuration)
			}

			// Run the editor and wait for it to complete
			cmd := exec.Command(editor, tmpFile)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			// Pause the program to allow the editor to run without interference
			err := m.program.ReleaseTerminal()
			if err != nil {
				return m, m.flashMsg.Flash("Error preparing to view series: "+err.Error(), internal.Error, flashDuration)
			}

			// Display the editor
			err = cmd.Run()

			// Restore terminal after editor closes
			restoreErr := m.program.RestoreTerminal()
			if restoreErr != nil {
				_ = level.Warn(m.logger).Log("msg", "Failed to restore terminal", "err", restoreErr)
			}

			// Ideally the temp file would be removed here but that causes issues with editors like vscode
			if err != nil {
				return m, m.flashMsg.Flash("Failed to run editor: "+err.Error(), internal.Error, flashDuration)
			}
			return m, nil
		case "/":
			m.search = searchAll
			m.searchInput.SetCursor(int(cursor.CursorBlink))
			m.searchInput.CursorEnd()
			return m, m.searchInput.Focus()
		case "m":
			m.search = searchMetrics
			m.searchInput.SetCursor(int(cursor.CursorBlink))
			m.searchInput.CursorEnd()
			return m, m.searchInput.Focus()
		case "l":
			m.search = searchLabels
			m.searchInput.SetCursor(int(cursor.CursorBlink))
			m.searchInput.CursorEnd()
			return m, m.searchInput.Focus()
		}
	}

	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *seriesTable) updateWhileSearchingMetrics(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if !m.searchInput.Focused() {
				// enter should allow viewing metrics for the filtered row that's selected
				return m.updateWhileBrowsingTable(msg)
			}

			// Allow exploring the filtered table
			m.searchInput.SetCursor(int(cursor.CursorHide))
			m.searchInput.Blur()
			m.table.Focus()
			return m, cmd
		case "esc":
			// Reset the search input and table back to their initial state
			m.searchInput.Reset()
			m.searchInput.Blur()
			m.setTableRows(noFiltering)

			// Hide the search input and restore control to the table
			m.search = searchNone
			m.table.Focus()
			return m, cmd
		default:
			if m.searchInput.Focused() {
				// Update the search value with the key press from this msg
				m.searchInput, cmd = m.searchInput.Update(msg)

				oldRowCount := len(m.table.Rows())
				switch m.search {
				case searchNone:
					// Show all rows
					m.setTableRows(noFiltering)
				case searchMetrics:
					m.setTableRows(func(info scrape.SeriesInfo) bool {
						return fuzzy.MatchFold(m.searchInput.Value(), info.Name)
					})
				case searchLabels:
					m.setTableRows(func(info scrape.SeriesInfo) bool {
						return fuzzy.MatchFold(m.searchInput.Value(), info.Labels)
					})
				case searchAll:
					m.setTableRows(func(info scrape.SeriesInfo) bool {
						metricMatch := fuzzy.MatchFold(m.searchInput.Value(), info.Name)
						labelMatch := fuzzy.MatchFold(m.searchInput.Value(), info.Labels)
						return metricMatch || labelMatch
					})
				}

				if oldRowCount != len(m.table.Rows()) {
					//Reset the selected row since the current index might exceed the filtered count
					m.table.SetCursor(0)
				}

				return m, cmd
			}
		}
	}

	if m.table.Focused() {
		// Allow navigating the filtered table
		return m.updateWhileBrowsingTable(msg)
	}

	m.searchInput, cmd = m.searchInput.Update(msg)
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
		scrapeFile := opts.ScrapeFile
		timeoutDuration := opts.Timeout
		httpConfigFile := opts.HttpConfigFile

		if scrapeURL == "" && scrapeFile == "" {
			return errors.New("No URL or file provided to scrape metrics. " +
				"Please supply a target to scrape via `--scrape.url` or `--scrape.file` flags.")
		}

		if scrapeURL != "" && scrapeFile != "" {
			return errors.New("The flags `--scrape.url` and `--scrape.file` are mutually exclusive.")
		}

		metricTable := newModel(nil, opts.OutputHeight, logger)
		p := tea.NewProgram(metricTable)
		metricTable.program = p

		// Create a channel to signal when scraping is complete
		scrapeDone := make(chan struct{})

		g.Add(func() error {
			_, err := p.Run()
			return err
		}, func(error) {
			close(scrapeDone)
		})

		g.Add(func() error {
			maxSize, err := opts.MaxScrapeSizeBytes()
			if err != nil {
				err = errors.Wrapf(err, "failed to parse max scrape size")
				p.Send(err)
				return err
			}

			level.Info(logger).Log(
				"msg", "scraping",
				"scrape_url", scrapeURL,
				"scrape_file", scrapeFile,
				"timeout", timeoutDuration,
				"max_size", maxSize,
				"http_config_file", httpConfigFile,
			)

			t0 := time.Now()
			scraper := scrape.NewPromScraper(
				scrapeURL,
				scrapeFile,
				logger,
				scrape.WithTimeout(timeoutDuration),
				scrape.WithMaxBodySize(maxSize),
				scrape.WithHttpConfigFile(httpConfigFile),
			)
			metrics, err := scraper.Scrape()
			if err != nil {
				p.Send(err)
				return err
			}

			// Send the scraped data to the UI
			level.Info(logger).Log("msg", "scraping complete", "duration", time.Since(t0))
			p.Send(metrics)
			return nil
		}, func(error) {})

		return nil
	})
}
