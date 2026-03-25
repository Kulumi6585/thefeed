package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sartoopjj/thefeed/internal/client"
	"github.com/sartoopjj/thefeed/internal/protocol"
	"github.com/sartoopjj/thefeed/internal/version"
)

var (
	channelListStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)

	messageViewStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62")).
				Padding(0, 1)

	logViewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Bold(true)

	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Background(lipgloss.Color("236")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true).
			Padding(0, 1)

	timestampStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	msgIDStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("69"))

	logDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

type focus int

const (
	focusChannels focus = iota
	focusMessages
	focusLog
)

const maxLogLines = 200

// logBuffer is a shared log buffer that survives bubbletea model copies.
type logBuffer struct {
	lines []string
}

func (lb *logBuffer) append(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("%s %s", logDimStyle.Render(ts), msg)
	lb.lines = append(lb.lines, line)
	if len(lb.lines) > maxLogLines {
		lb.lines = lb.lines[len(lb.lines)-maxLogLines:]
	}
}

// Model is the TUI state.
type Model struct {
	fetcher  *client.Fetcher
	cache    *client.Cache
	channels []protocol.ChannelInfo
	messages map[int][]protocol.Message

	selectedChan int
	focus        focus
	viewport     viewport.Model
	logViewport  viewport.Model

	width, height   int
	status          string
	loading         bool
	forceRefresh    bool
	err             error
	lastUpdate      time.Time
	serverTimestamp uint32
	marker          [protocol.MarkerSize]byte
	resolverInfo    string
	logBuf          *logBuffer

	autoRefreshInterval time.Duration
}

type (
	metadataMsg struct {
		meta *protocol.Metadata
		err  error
	}
	channelDataMsg struct {
		channelNum int
		msgs       []protocol.Message
		err        error
	}
	tickMsg struct{}
	logMsg  string
)

// New creates a new TUI model.
func New(fetcher *client.Fetcher, cache *client.Cache) Model {
	vp := viewport.New(0, 0)
	lv := viewport.New(0, 0)
	lb := &logBuffer{}
	m := Model{
		fetcher:             fetcher,
		cache:               cache,
		messages:            make(map[int][]protocol.Message),
		viewport:            vp,
		logViewport:         lv,
		logBuf:              lb,
		autoRefreshInterval: 30 * time.Second,
		status:              "Starting...",
		resolverInfo:        strings.Join(fetcher.Resolvers(), ", "),
	}

	// Set up fetcher log callback — uses shared pointer so it works
	// even after bubbletea copies the Model struct.
	fetcher.SetLogFunc(func(msg string) {
		lb.append(msg)
	})

	return m
}

// Init starts the TUI.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchMetadata(),
		m.tickCmd(),
	)
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateViewportSize()

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "left", "right":
			// Cycle: channels → messages → log → channels
			switch m.focus {
			case focusChannels:
				m.focus = focusMessages
			case focusMessages:
				m.focus = focusLog
			case focusLog:
				m.focus = focusChannels
			}
		case "up", "k":
			if m.focus == focusChannels {
				if m.selectedChan > 0 {
					m.selectedChan--
					cmds = append(cmds, m.loadChannel(m.selectedChan+1))
				}
			} else if m.focus == focusMessages {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				var cmd tea.Cmd
				m.logViewport, cmd = m.logViewport.Update(msg)
				cmds = append(cmds, cmd)
			}
		case "down", "j":
			if m.focus == focusChannels {
				if m.selectedChan < len(m.channels)-1 {
					m.selectedChan++
					cmds = append(cmds, m.loadChannel(m.selectedChan+1))
				}
			} else if m.focus == focusMessages {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				var cmd tea.Cmd
				m.logViewport, cmd = m.logViewport.Update(msg)
				cmds = append(cmds, cmd)
			}
		case "r":
			m.status = "Refreshing..."
			m.loading = true
			m.forceRefresh = true
			cmds = append(cmds, m.fetchMetadata())
		case "pgup", "pgdown", "home", "end":
			if m.focus == focusLog {
				var cmd tea.Cmd
				m.logViewport, cmd = m.logViewport.Update(msg)
				cmds = append(cmds, cmd)
			} else {
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

	case metadataMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			m.status = fmt.Sprintf("Error: %v", msg.err)
		} else {
			m.channels = msg.meta.Channels
			m.serverTimestamp = msg.meta.Timestamp
			m.marker = msg.meta.Marker
			m.lastUpdate = time.Now()
			m.err = nil
			m.status = fmt.Sprintf("Updated %s | Server: %s",
				time.Now().Format("15:04:05"),
				time.Unix(int64(m.serverTimestamp), 0).Format("15:04:05"))

			_ = m.cache.PutMetadata(msg.meta)

			if len(m.channels) > 0 {
				// Always fetch fresh data after metadata update
				cmds = append(cmds, m.loadChannelFresh(m.selectedChan+1))
			}
			m.forceRefresh = false
		}

	case channelDataMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Channel error: %v", msg.err)
		} else {
			m.messages[msg.channelNum] = msg.msgs
			_ = m.cache.PutMessages(msg.channelNum, msg.msgs)
			m.updateViewportContent()
		}

	case tickMsg:
		cmds = append(cmds, m.tickCmd())
		// Update log viewport content
		m.updateLogContent()
		if time.Since(m.lastUpdate) > m.autoRefreshInterval && !m.loading {
			m.loading = true
			m.status = "Auto-refreshing..."
			cmds = append(cmds, m.fetchMetadata())
		}

	case logMsg:
		m.logBuf.append(string(msg))
		m.updateLogContent()
	}

	return m, tea.Batch(cmds...)
}

// View renders the TUI.
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	channelWidth := m.width / 4
	if channelWidth < 20 {
		channelWidth = 20
	}
	if channelWidth > 40 {
		channelWidth = 40
	}
	messageWidth := m.width - channelWidth - 4

	// Split height: messages get 80%, log panel gets 20% of content area
	contentHeight := m.height - 3
	if contentHeight < 10 {
		contentHeight = 10
	}
	msgHeight := contentHeight * 8 / 10
	logHeight := contentHeight - msgHeight
	if logHeight < 3 {
		logHeight = 3
		msgHeight = contentHeight - logHeight
	}

	channelContent := m.renderChannelList(channelWidth-4, msgHeight-2)
	borderColor := "62"
	if m.focus == focusChannels {
		borderColor = "229"
	}
	channelPanel := channelListStyle.
		BorderForeground(lipgloss.Color(borderColor)).
		Width(channelWidth - 2).
		Height(msgHeight).
		Render(channelContent)

	m.updateViewportContent()
	messageTitle := " Messages "
	if m.selectedChan < len(m.channels) {
		ch := m.channels[m.selectedChan]
		messageTitle = fmt.Sprintf(" %s (%d blocks) ", ch.Name, ch.Blocks)
	}
	messageContent := titleStyle.Render(messageTitle) + "\n" + m.viewport.View()
	msgBorderColor := "62"
	if m.focus == focusMessages {
		msgBorderColor = "229"
	}
	messagePanel := messageViewStyle.
		BorderForeground(lipgloss.Color(msgBorderColor)).
		Width(messageWidth - 2).
		Height(msgHeight).
		Render(messageContent)

	topContent := lipgloss.JoinHorizontal(lipgloss.Top, channelPanel, messagePanel)

	// Log panel (full width)
	m.updateLogContent()
	logBorderColor := "240"
	if m.focus == focusLog {
		logBorderColor = "229"
	}
	logTitle := titleStyle.Render(" Log ")
	logContent := logTitle + "\n" + m.logViewport.View()
	logPanel := logViewStyle.
		BorderForeground(lipgloss.Color(logBorderColor)).
		Width(m.width - 4).
		Height(logHeight).
		Render(logContent)

	// Status bar
	statusLeft := m.status
	if m.loading {
		statusLeft = "... " + statusLeft
	}
	resolverStr := ""
	if m.resolverInfo != "" {
		resolverStr = " | DNS: " + truncateStr(m.resolverInfo, 30)
	}
	versionStr := ""
	if version.Version != "" {
		versionStr = " v" + version.Version
	}
	statusRight := fmt.Sprintf("Tab/←→:switch j/k:nav r:refresh q:quit%s%s", resolverStr, versionStr)

	gap := m.width - utf8.RuneCountInString(statusLeft) - utf8.RuneCountInString(statusRight) - 2
	if gap < 1 {
		gap = 1
	}
	statusBar := statusStyle.Width(m.width).Render(
		statusLeft + strings.Repeat(" ", gap) + statusRight,
	)

	return topContent + "\n" + logPanel + "\n" + statusBar
}

func (m Model) renderChannelList(width, height int) string {
	title := titleStyle.Render(" Channels ")
	var lines []string
	lines = append(lines, title)

	if len(m.channels) == 0 {
		lines = append(lines, "  No channels")
		return strings.Join(lines, "\n")
	}

	for i, ch := range m.channels {
		prefix := "  "
		style := normalStyle
		if i == m.selectedChan {
			prefix = "> "
			if m.focus == focusChannels {
				style = selectedStyle
			} else {
				style = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
			}
		}
		name := truncateStr(ch.Name, width-4)
		line := style.Render(fmt.Sprintf("%s%d. %s", prefix, i+1, name))
		lines = append(lines, line)
	}

	for len(lines) < height {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

// wrapText wraps a line to fit within maxWidth, returning multiple lines.
func wrapText(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= maxWidth {
		return []string{s}
	}
	var lines []string
	for len(runes) > maxWidth {
		// Try to break at a space within the last portion
		breakAt := maxWidth
		for i := maxWidth; i > maxWidth/2; i-- {
			if runes[i] == ' ' {
				breakAt = i
				break
			}
		}
		lines = append(lines, string(runes[:breakAt]))
		runes = runes[breakAt:]
		// Skip leading space on next line
		if len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	if len(runes) > 0 {
		lines = append(lines, string(runes))
	}
	return lines
}

func (m *Model) updateViewportContent() {
	chNum := m.selectedChan + 1
	msgs, ok := m.messages[chNum]
	if !ok {
		cached := m.cache.GetMessages(chNum, 5*time.Minute)
		if cached != nil {
			m.messages[chNum] = cached
			msgs = cached
		}
	}

	if len(msgs) == 0 {
		m.viewport.SetContent("  No messages yet. Press r to refresh.")
		return
	}

	wrapWidth := m.viewport.Width - 4
	if wrapWidth < 20 {
		wrapWidth = 20
	}

	var lines []string
	for _, msg := range msgs {
		ts := time.Unix(int64(msg.Timestamp), 0).Format("15:04 Jan 02")
		header := fmt.Sprintf("%s  %s",
			timestampStyle.Render(ts),
			msgIDStyle.Render(fmt.Sprintf("#%d", msg.ID)))

		lines = append(lines, header)
		for _, textLine := range strings.Split(msg.Text, "\n") {
			wrapped := wrapText(textLine, wrapWidth-2)
			for _, wl := range wrapped {
				lines = append(lines, "  "+wl)
			}
		}
		lines = append(lines, "")
	}

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m *Model) updateLogContent() {
	if len(m.logBuf.lines) == 0 {
		m.logViewport.SetContent("  Waiting for DNS queries...")
		return
	}
	content := strings.Join(m.logBuf.lines, "\n")
	m.logViewport.SetContent(content)
	m.logViewport.GotoBottom()
}

func (m *Model) updateViewportSize() {
	channelWidth := m.width / 4
	if channelWidth < 20 {
		channelWidth = 20
	}
	if channelWidth > 40 {
		channelWidth = 40
	}
	messageWidth := m.width - channelWidth - 4

	contentHeight := m.height - 3
	if contentHeight < 10 {
		contentHeight = 10
	}
	msgHeight := contentHeight * 8 / 10
	logHeight := contentHeight - msgHeight
	if logHeight < 3 {
		logHeight = 3
		msgHeight = contentHeight - logHeight
	}

	m.viewport.Width = messageWidth - 4
	m.viewport.Height = msgHeight - 4
	if m.viewport.Height < 1 {
		m.viewport.Height = 1
	}

	m.logViewport.Width = m.width - 8
	m.logViewport.Height = logHeight - 4
	if m.logViewport.Height < 1 {
		m.logViewport.Height = 1
	}
}

func (m *Model) fetchMetadata() tea.Cmd {
	return func() tea.Msg {
		meta, err := m.fetcher.FetchMetadata()
		return metadataMsg{meta: meta, err: err}
	}
}

func (m *Model) loadChannel(channelNum int) tea.Cmd {
	if !m.forceRefresh {
		if msgs := m.cache.GetMessages(channelNum, 1*time.Minute); msgs != nil {
			return func() tea.Msg {
				return channelDataMsg{channelNum: channelNum, msgs: msgs}
			}
		}
	}

	blockCount := 0
	idx := channelNum - 1
	if idx >= 0 && idx < len(m.channels) {
		blockCount = int(m.channels[idx].Blocks)
	}

	return func() tea.Msg {
		msgs, err := m.fetcher.FetchChannel(channelNum, blockCount)
		return channelDataMsg{channelNum: channelNum, msgs: msgs, err: err}
	}
}

func (m *Model) loadChannelFresh(channelNum int) tea.Cmd {
	blockCount := 0
	idx := channelNum - 1
	if idx >= 0 && idx < len(m.channels) {
		blockCount = int(m.channels[idx].Blocks)
	}

	return func() tea.Msg {
		msgs, err := m.fetcher.FetchChannel(channelNum, blockCount)
		return channelDataMsg{channelNum: channelNum, msgs: msgs, err: err}
	}
}

func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

func truncateStr(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen-1]) + "..."
}

// Run starts the TUI application.
func Run(fetcher *client.Fetcher, cache *client.Cache) error {
	m := New(fetcher, cache)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
