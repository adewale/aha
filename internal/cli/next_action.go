package cli

import (
	"regexp"
	"strings"
)

type nextAction struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

func (a nextAction) String() string {
	parts := []string{shellQuote(a.Command)}
	for _, arg := range a.Args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

var shellSafeArg = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

func shellQuote(value string) string {
	if shellSafeArg.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
