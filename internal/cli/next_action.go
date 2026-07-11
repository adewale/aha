package cli

import (
	"regexp"
	"strings"

	"github.com/adewale/aha/internal/depot"
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

func actionOutput(action nextAction) ([]string, nextAction) {
	return []string{action.String()}, action
}

func actionWithConfig(configPath string, args ...string) nextAction {
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	return nextAction{Command: "aha", Args: args}
}

func doctorNextAction(configPath string, configExists bool, cfgErr error, depotDiag, corpusDiag map[string]any) nextAction {
	withConfig := func(args ...string) nextAction { return actionWithConfig(configPath, args...) }
	if !configExists {
		return withConfig("init", "--accept-secrets")
	}
	if cfgErr != nil {
		// Never suggest `init`: it would either fail on or encourage replacing
		// the malformed existing config. Doctor remains the read-only repair
		// surface and reports the parse error alongside this action.
		return withConfig("doctor")
	}
	depotType, _ := depotDiag["type"].(string)
	depotLocation, _ := depotDiag["location"].(string)
	depotAddress := (depot.Address{Type: depotType, Location: depotLocation}).String()
	if _, hasError := depotDiag["error"]; hasError {
		if depotType == "r2" && depotLocation != "" {
			return withConfig("depot", "setup", depotAddress)
		}
		return withConfig("doctor")
	}
	if initialized, ok := depotDiag["initialized"].(bool); ok && !initialized {
		return withConfig("depot", "init", depotAddress)
	}
	if ok, present := depotDiag["ok"].(bool); present && !ok {
		return withConfig("depot", "verify", depotAddress, "--deep")
	}
	if legacy, _ := corpusDiag["legacy"].(bool); legacy {
		return withConfig("corpus", "rebuild", "--backup")
	}
	if ok, _ := corpusDiag["ok"].(bool); !ok {
		return withConfig("refresh", "--max-sessions", "1")
	}
	if snapshots, _ := corpusDiag["snapshots"].(int); snapshots == 0 {
		return withConfig("refresh", "--max-sessions", "1")
	}
	return withConfig("search", "migration", "--refs")
}

func depotSetupAction(address, configPath string, selected bool, diag map[string]any) (string, nextAction) {
	withConfig := func(args ...string) nextAction { return actionWithConfig(configPath, args...) }
	if _, failed := diag["error"]; failed {
		return "blocked", withConfig("depot", "setup", address)
	}
	if initialized, ok := diag["initialized"].(bool); ok && !initialized {
		return "uninitialized", withConfig("depot", "init", address)
	}
	if ok, present := diag["ok"].(bool); present && !ok {
		return "degraded", withConfig("depot", "verify", address, "--deep")
	}
	if !selected {
		return "ready-not-selected", withConfig("depot", "use", address)
	}
	return "ready", withConfig("refresh", "--max-sessions", "1")
}
