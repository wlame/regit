package ui

import "github.com/charmbracelet/lipgloss"

// styles centralizes every lipgloss style the TUI uses so the look is consistent
// and easy to retheme. Colors use the 256-color palette for broad terminal
// support.
type styles struct {
	app          lipgloss.Style
	title        lipgloss.Style
	pane         lipgloss.Style
	paneActive   lipgloss.Style
	listCursor   lipgloss.Style
	hash         lipgloss.Style
	author       lipgloss.Style
	dayHighlight lipgloss.Style
	picked       lipgloss.Style
	available    lipgloss.Style // days inside a selection's segment windows
	newDate      lipgloss.Style
	marker       lipgloss.Style
	statusOK     lipgloss.Style
	statusWarn   lipgloss.Style
	statusErr    lipgloss.Style
	keybar       lipgloss.Style
	modal        lipgloss.Style
	modalTitle   lipgloss.Style
	selected     lipgloss.Style
	dim          lipgloss.Style
	help         lipgloss.Style

	// heatLevels are the five GitHub-style contribution shades, from empty to
	// most intense.
	heatLevels []lipgloss.Style
	heatCursor lipgloss.Style
}

func newStyles() styles {
	return styles{
		app:          lipgloss.NewStyle().Padding(0, 1),
		title:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63")).Padding(0, 1),
		pane:         lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1),
		paneActive:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(0, 1),
		listCursor:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
		hash:         lipgloss.NewStyle().Foreground(lipgloss.Color("178")),
		author:       lipgloss.NewStyle().Foreground(lipgloss.Color("109")),
		dayHighlight: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("60")),
		picked:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("84")),
		available: lipgloss.NewStyle().
			Foreground(lipgloss.Color("114")).Background(lipgloss.Color("22")),
		newDate:    lipgloss.NewStyle().Foreground(lipgloss.Color("84")),
		marker:     lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		statusOK:   lipgloss.NewStyle().Foreground(lipgloss.Color("84")),
		statusWarn: lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		statusErr:  lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		keybar:     lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		modal:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("63")).Padding(1, 2),
		modalTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
		selected:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("63")),
		dim:        lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		help:       lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		heatLevels: []lipgloss.Style{
			lipgloss.NewStyle().Foreground(lipgloss.Color("236")), // empty
			lipgloss.NewStyle().Foreground(lipgloss.Color("22")),  // light
			lipgloss.NewStyle().Foreground(lipgloss.Color("28")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("34")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("46")), // most
		},
		heatCursor: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")),
	}
}
