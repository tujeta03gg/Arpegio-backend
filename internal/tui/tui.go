// Package tui is the terminal frontend: a thin Bubble Tea view over the daemon's API.
// It keeps no state of its own beyond what is on screen; the daemon is the source of truth.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"arpegio/internal/api"
	"arpegio/internal/client"
)

type tab int

const (
	tabNow tab = iota
	tabLibrary
	tabSearch
	tabQueue
	tabCount
)

var tabNames = [tabCount]string{"Now playing", "Library", "Search", "Queue"}

// ---- messages ----

type (
	playerMsg       api.PlayerDTO
	queueMsg        api.QueueDTO
	albumsMsg       []api.AlbumDTO
	tracksMsg       []api.TrackDTO
	errMsg          struct{ err error }
	eventMsg        client.Event
	subscribedMsg   struct{ ch <-chan client.Event }
	streamClosedMsg struct{}
)

type Model struct {
	ctx    context.Context
	c      *client.Client
	events <-chan client.Event

	tab           tab
	width, height int

	player api.PlayerDTO
	queue  api.QueueDTO

	albums   []api.AlbumDTO
	albumCur int

	search     textinput.Model
	typing     bool
	results    []api.TrackDTO
	resultsCur int

	queueCur int
	status   string
}

// New builds the model. ctx ends the event subscription.
func New(ctx context.Context, c *client.Client) Model {
	in := textinput.New()
	in.Placeholder = "search tracks, albums, artists"
	in.Prompt = "/ "
	return Model{ctx: ctx, c: c, search: in, width: 80, height: 24}
}

// ---- commands (each talks to the daemon and returns one message) ----

func (m Model) call(method, path string, body any) tea.Cmd {
	return func() tea.Msg {
		var p api.PlayerDTO
		if err := m.c.Do(m.ctx, method, path, body, &p); err != nil {
			return errMsg{err}
		}
		return playerMsg(p)
	}
}

func (m Model) fetchQueue() tea.Cmd {
	return func() tea.Msg {
		var q api.QueueDTO
		if err := m.c.Do(m.ctx, "GET", "/queue", nil, &q); err != nil {
			return errMsg{err}
		}
		return queueMsg(q)
	}
}

func (m Model) fetchAlbums() tea.Cmd {
	return func() tea.Msg {
		var p api.Paged[api.AlbumDTO]
		if err := m.c.Do(m.ctx, "GET", "/albums?limit=500", nil, &p); err != nil {
			return errMsg{err}
		}
		return albumsMsg(p.Items)
	}
}

func (m Model) runSearch(text string) tea.Cmd {
	return func() tea.Msg {
		var res map[string][]api.HitDTO
		if err := m.c.Do(m.ctx, "GET", "/search?types=track&limit=50&q="+url.QueryEscape(text), nil, &res); err != nil {
			return errMsg{err}
		}
		out := make([]api.TrackDTO, len(res["tracks"]))
		for i, h := range res["tracks"] {
			out[i] = api.TrackDTO{ID: h.ID, Title: h.Title, Artist: h.Artist, Album: h.Album}
		}
		return tracksMsg(out)
	}
}

func (m Model) addToQueue(body map[string]any) tea.Cmd {
	return func() tea.Msg {
		var q api.QueueDTO
		if err := m.c.Do(m.ctx, "POST", "/queue", body, &q); err != nil {
			return errMsg{err}
		}
		return queueMsg(q)
	}
}

func (m Model) queueCall(method, path string, body any) tea.Cmd {
	return func() tea.Msg {
		var q api.QueueDTO
		if err := m.c.Do(m.ctx, method, path, body, &q); err != nil {
			return errMsg{err}
		}
		return queueMsg(q)
	}
}

func (m Model) subscribe() tea.Cmd {
	return func() tea.Msg {
		ch, err := m.c.Subscribe(m.ctx)
		if err != nil {
			return errMsg{err}
		}
		return subscribedMsg{ch}
	}
}

func waitEvent(ch <-chan client.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return eventMsg(ev)
	}
}

// ---- bubbletea ----

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.call("GET", "/player", nil), m.fetchQueue(), m.fetchAlbums(), m.subscribe())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case playerMsg:
		m.player = api.PlayerDTO(msg)
	case queueMsg:
		m.queue = api.QueueDTO(msg)
		m.queueCur = clamp(m.queueCur, len(m.queue.Items))
	case albumsMsg:
		m.albums = msg
		m.albumCur = clamp(m.albumCur, len(m.albums))
	case tracksMsg:
		m.results, m.resultsCur = msg, 0
		m.status = fmt.Sprintf("%d results", len(msg))
	case errMsg:
		m.status = "error: " + msg.err.Error()
	case subscribedMsg:
		m.events = msg.ch
		return m, waitEvent(m.events)
	case streamClosedMsg:
		m.status = "disconnected from arpegiod"
		m.events = nil
	case eventMsg:
		return m.onEvent(client.Event(msg))
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

// onEvent folds a server push into the model and keeps listening.
func (m Model) onEvent(ev client.Event) (tea.Model, tea.Cmd) {
	next := waitEvent(m.events)
	switch ev.Type {
	case "snapshot":
		var s struct {
			Player api.PlayerDTO `json:"player"`
			Queue  api.QueueDTO  `json:"queue"`
		}
		if json.Unmarshal(ev.Data, &s) == nil {
			m.player, m.queue = s.Player, s.Queue
		}
	case "player.state_changed":
		var p api.PlayerDTO
		if json.Unmarshal(ev.Data, &p) == nil {
			m.player = p
		}
	case "player.position":
		var p struct {
			PositionMS int64 `json:"position_ms"`
			DurationMS int64 `json:"duration_ms"`
		}
		if json.Unmarshal(ev.Data, &p) == nil {
			m.player.PositionMS, m.player.DurationMS = p.PositionMS, p.DurationMS
		}
	case "queue.changed":
		return m, tea.Batch(next, m.fetchQueue())
	case "catalog.updated", "library.scan_finished":
		return m, tea.Batch(next, m.fetchAlbums())
	}
	return m, next
}

func (m Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.typing {
		return m.onTyping(k)
	}
	key := k.String()
	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		m.tab = (m.tab + 1) % tabCount
		return m, nil
	case "shift+tab":
		m.tab = (m.tab + tabCount - 1) % tabCount
		return m, nil
	case "1", "2", "3", "4":
		m.tab = tab(key[0] - '1')
		return m, nil
	case " ":
		if m.player.Playing {
			return m, m.call("POST", "/player/pause", nil)
		}
		return m, m.call("POST", "/player/play", nil)
	case "n":
		return m, m.call("POST", "/player/next", nil)
	case "p":
		return m, m.call("POST", "/player/previous", nil)
	case "+", "=":
		return m, m.call("PUT", "/player/volume", map[string]int{"level": min(100, m.player.Volume+5)})
	case "-":
		return m, m.call("PUT", "/player/volume", map[string]int{"level": max(0, m.player.Volume-5)})
	case "s":
		return m, m.call("PUT", "/player/shuffle", map[string]bool{"enabled": !m.player.Shuffle})
	case "r":
		return m, m.call("PUT", "/player/repeat", map[string]string{"mode": nextRepeat(m.player.Repeat)})
	case "/":
		m.tab, m.typing = tabSearch, true
		m.search.Focus()
		return m, textinput.Blink
	}
	switch m.tab {
	case tabLibrary:
		return m.onLibraryKey(key)
	case tabSearch:
		return m.onSearchKey(key)
	case tabQueue:
		return m.onQueueKey(key)
	}
	return m, nil
}

func nextRepeat(cur string) string {
	switch cur {
	case "off", "":
		return "all"
	case "all":
		return "one"
	}
	return "off"
}

func (m Model) onTyping(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.typing = false
		m.search.Blur()
		return m, nil
	case "enter":
		m.typing = false
		m.search.Blur()
		if text := strings.TrimSpace(m.search.Value()); text != "" {
			return m, m.runSearch(text)
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(k)
	return m, cmd
}

func (m Model) onLibraryKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.albumCur = clamp(m.albumCur-1, len(m.albums))
	case "down", "j":
		m.albumCur = clamp(m.albumCur+1, len(m.albums))
	case "enter":
		if len(m.albums) > 0 {
			return m, m.call("POST", "/player/play", map[string]string{"context": "album:" + m.albums[m.albumCur].ID})
		}
	case "a":
		if len(m.albums) > 0 {
			return m, m.addToQueue(map[string]any{"context": "album:" + m.albums[m.albumCur].ID})
		}
	}
	return m, nil
}

func (m Model) onSearchKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.resultsCur = clamp(m.resultsCur-1, len(m.results))
	case "down", "j":
		m.resultsCur = clamp(m.resultsCur+1, len(m.results))
	case "enter":
		if len(m.results) > 0 {
			return m, m.call("POST", "/player/play", map[string]string{"context": "track:" + m.results[m.resultsCur].ID})
		}
	case "a":
		if len(m.results) > 0 {
			return m, m.addToQueue(map[string]any{"track_ids": []string{m.results[m.resultsCur].ID}})
		}
	}
	return m, nil
}

func (m Model) onQueueKey(key string) (tea.Model, tea.Cmd) {
	items := m.queue.Items
	switch key {
	case "up", "k":
		m.queueCur = clamp(m.queueCur-1, len(items))
	case "down", "j":
		m.queueCur = clamp(m.queueCur+1, len(items))
	case "d":
		if len(items) > 0 {
			return m, m.queueCall("DELETE", fmt.Sprintf("/queue/%v", items[m.queueCur].Position), nil)
		}
	case "c":
		return m, m.queueCall("DELETE", "/queue", nil)
	case "K", "J": // move the highlighted item up or down one place
		from, to := m.queueCur, m.queueCur-1
		if key == "J" {
			to = from + 1
		}
		if len(items) > 0 && to >= 0 && to < len(items) {
			m.queueCur = to
			return m, m.queueCall("PATCH", fmt.Sprintf("/queue/%v", items[from].Position), map[string]int{"index": to})
		}
	}
	return m, nil
}

func clamp(i, n int) int {
	if n == 0 {
		return 0
	}
	return max(0, min(i, n-1))
}

// ---- view ----

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	activeTab   = lipgloss.NewStyle().Bold(true).Underline(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	cursorStyle = lipgloss.NewStyle().Reverse(true)
)

func (m Model) View() string {
	var b strings.Builder
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, name)
		if tab(i) == m.tab {
			label = activeTab.Render(label)
		} else {
			label = dimStyle.Render(label)
		}
		b.WriteString(label)
	}
	b.WriteString("\n\n")

	switch m.tab {
	case tabNow:
		b.WriteString(m.viewNow())
	case tabLibrary:
		rows := make([]string, len(m.albums))
		for i, a := range m.albums {
			rows[i] = a.Title
			if a.Artist != "" {
				rows[i] += " — " + a.Artist
			}
		}
		b.WriteString(m.list(rows, m.albumCur, "no albums yet: run `arpegio scan`"))
	case tabSearch:
		b.WriteString(m.search.View() + "\n\n")
		rows := make([]string, len(m.results))
		for i, t := range m.results {
			rows[i] = trackLine(t)
		}
		b.WriteString(m.list(rows, m.resultsCur, "press / to search"))
	case tabQueue:
		rows := make([]string, len(m.queue.Items))
		for i, it := range m.queue.Items {
			mark := "  "
			if it.Current {
				mark = "▶ "
			}
			rows[i] = mark + trackLine(it.Track)
			if it.Priority == 1 {
				rows[i] += "  [next]"
			}
		}
		b.WriteString(m.list(rows, m.queueCur, "queue is empty"))
	}

	b.WriteString("\n\n" + dimStyle.Render(m.help()))
	if m.status != "" {
		b.WriteString("\n" + m.status)
	}
	return b.String()
}

func (m Model) viewNow() string {
	p := m.player
	if p.Track == nil {
		return dimStyle.Render("nothing playing") + "\n" + m.modes()
	}
	icon := "⏸"
	if p.Playing {
		icon = "▶"
	}
	return fmt.Sprintf("%s %s\n  %s — %s\n\n  %s  %s / %s\n\n%s",
		icon, titleStyle.Render(p.Track.Title), p.Track.Artist, p.Track.Album,
		progress(p.PositionMS, p.DurationMS, max(10, m.width-24)), clock(p.PositionMS), clock(p.DurationMS), m.modes())
}

func (m Model) modes() string {
	return fmt.Sprintf("  vol %d   shuffle %s   repeat %s", m.player.Volume, map[bool]string{true: "on", false: "off"}[m.player.Shuffle], m.player.Repeat)
}

func (m Model) help() string {
	return "space play/pause · n/p next/prev · +/- volume · s shuffle · r repeat · tab switch · / search · q quit"
}

func (m Model) list(rows []string, cur int, empty string) string {
	if len(rows) == 0 {
		return dimStyle.Render(empty)
	}
	visible := max(3, m.height-8)
	start := max(0, min(cur-visible/2, len(rows)-visible))
	end := min(len(rows), start+visible)
	var b strings.Builder
	for i := start; i < end; i++ {
		line := truncate(rows[i], max(10, m.width-2))
		if i == cur {
			line = cursorStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func progress(pos, dur int64, width int) string {
	if dur <= 0 {
		return strings.Repeat("─", width)
	}
	filled := int(int64(width) * min(pos, dur) / dur)
	return strings.Repeat("━", filled) + strings.Repeat("─", width-filled)
}

func clock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func trackLine(t api.TrackDTO) string {
	s := t.Title
	if t.Artist != "" {
		s += " — " + t.Artist
	}
	if t.Album != "" {
		s += " (" + t.Album + ")"
	}
	return s
}

func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width-1]) + "…"
}

// Run starts the interactive program and blocks until the user quits.
func Run(ctx context.Context, c *client.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err := tea.NewProgram(New(ctx, c), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}
