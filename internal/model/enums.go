package model

import "strings"

type Role string

const (
	RoleUser              Role = "user"
	RoleAssistant         Role = "assistant"
	RoleBranchSummary     Role = "branchSummary"
	RoleCompactionSummary Role = "compactionSummary"
	RoleSummary           Role = "summary"
	RoleToolResult        Role = "toolResult"
	RoleBashExecution     Role = "bashExecution"
)

func ParseRole(s string) Role { return Role(s) }

func ShouldIndexRoleText(role Role, text string, indexToolOutput bool) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	switch role {
	case RoleUser, RoleAssistant, RoleBranchSummary, RoleCompactionSummary, RoleSummary:
		return true
	case RoleToolResult, RoleBashExecution:
		return indexToolOutput
	default:
		return false
	}
}
