package ui

import (
	"reflect"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// keyinput.go makes Shift+Enter usable as the "new line" key.
//
// A terminal normally sends the same byte for Enter and Shift+Enter, so a
// program cannot tell them apart. Many terminals send a longer escape sequence
// for Shift+Enter once the program asks them to report modified keys. Bubble
// Tea does not know that sequence, so this file asks for it and translates it.

const (
	// ReportModifiedKeys asks the terminal to report keys such as Shift+Enter
	// with their own escape sequence (xterm's "modifyOtherKeys" level 1). At
	// this level Enter, Tab, Esc, Ctrl+letter and Alt+letter keep their usual
	// bytes. A terminal that does not know the request ignores it.
	ReportModifiedKeys = "\x1b[>4;1m"
	// StopReportingModifiedKeys puts the terminal back as it was. Write it
	// when the TUI exits.
	StopReportingModifiedKeys = "\x1b[>4;0m"
)

// shiftEnterSequences are the escape sequences terminals send for Shift+Enter:
// the xterm "modifyOtherKeys" form, and the "CSI u" form some terminals send
// when configured to.
var shiftEnterSequences = []string{"\x1b[27;2;13~", "\x1b[13;2u"}

// newLineKeys are the keys that add a line in a multi-line field, by the name
// Bubble Tea gives them. Shift+Enter arrives as ctrl+j after
// normalizeNewLineKey. ctrl+j and alt+enter work in every terminal, so they are
// the fallback where Shift+Enter is not reported.
var newLineKeys = map[string]bool{
	"ctrl+j":    true,
	"alt+enter": true,
}

// normalizeNewLineKey turns a Shift+Enter escape sequence into the ctrl+j key
// message, and returns every other message unchanged.
//
// Bubble Tea delivers an escape sequence it does not know as a message of an
// unexported type whose underlying type is a byte slice. A type switch cannot
// name an unexported type from another package, so this reads the bytes
// through reflection instead (TestShiftEnter_realTerminalBytesBecomeANewLineKey
// pins that behavior against the real input reader).
func normalizeNewLineKey(msg tea.Msg) tea.Msg {
	value := reflect.ValueOf(msg)
	isByteSlice := value.IsValid() && value.Kind() == reflect.Slice &&
		value.Type().Elem().Kind() == reflect.Uint8
	if !isByteSlice {
		return msg
	}
	if slices.Contains(shiftEnterSequences, string(value.Bytes())) {
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	}
	return msg
}
