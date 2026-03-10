package dashboard

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Data provider interface to decouple from component
type DataProvider interface {
	GetDashboardData() Data
}

// Data structure for bubbletea
type Data struct {
	TotalRooms   int
	TotalPlayers int
	Rooms        []RoomInfo
	Events       []string
}

type RoomInfo struct {
	ID          string
	Name        string
	PlayerCount int
	MaxPlayers  int
	Players     []PlayerInfo
}

type PlayerInfo struct {
	Name string
	X    float32
	Y    float32
	Z    float32
}

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1).
			Bold(true)

	statsStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A0A0A0")).
			MarginLeft(1)

	roomStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD")).
			Padding(0, 1).
			MarginTop(1)

	eventStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#F25D94")).
			Padding(0, 1).
			MarginTop(1)

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555555"))
)

type model struct {
	provider DataProvider
	data     Data
}

type tickMsg time.Time

func initialModel(provider DataProvider) model {
	return model{
		provider: provider,
		data:     provider.GetDashboardData(),
	}
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tickMsg:
		m.data = m.provider.GetDashboardData()
		return m, tickCmd()
	}
	return m, nil
}

func (m model) View() string {
	s := strings.Builder{}

	// Title
	s.WriteString(titleStyle.Render("KCP Game Server Dashboard"))
	s.WriteString("\n\n")

	// Global Stats
	stats := fmt.Sprintf("Active Rooms: %d | Online Players: %d | Time: %s",
		m.data.TotalRooms,
		m.data.TotalPlayers,
		time.Now().Format("15:04:05"),
	)
	s.WriteString(statsStyle.Render(stats))
	s.WriteString("\n")

	// Rooms
	var roomViews []string
	if len(m.data.Rooms) == 0 {
		roomViews = append(roomViews, subtleStyle.Render("(No active rooms)"))
	} else {
		for _, r := range m.data.Rooms {
			roomContent := fmt.Sprintf("Room: %s [%s]\nPlayers: %d/%d\n", r.Name, r.ID, r.PlayerCount, r.MaxPlayers)

			if len(r.Players) > 0 {
				roomContent += subtleStyle.Render(strings.Repeat("-", 30)) + "\n"
				for _, p := range r.Players {
					roomContent += fmt.Sprintf("  %s (%.1f, %.1f, %.1f)\n", p.Name, p.X, p.Y, p.Z)
				}
			} else {
				roomContent += subtleStyle.Render("  (no players)")
			}
			roomViews = append(roomViews, roomStyle.Render(roomContent))
		}
	}
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, roomViews...))
	s.WriteString("\n")

	// Events
	eventContent := "Recent Events:\n"
	if len(m.data.Events) == 0 {
		eventContent += subtleStyle.Render("(No events yet)")
	} else {
		for _, e := range m.data.Events {
			eventContent += fmt.Sprintf("> %s\n", e)
		}
	}
	s.WriteString(eventStyle.Render(eventContent))

	return s.String()
}

func tickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func Start(provider DataProvider) {
	p := tea.NewProgram(initialModel(provider), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
	}
}
