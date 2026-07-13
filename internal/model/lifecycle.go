package model

// Action is one complete, state-derived command transition.
type Action struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type ArchiveState string

const (
	ArchiveInvalidAddress       ArchiveState = "invalid_address"
	ArchiveInvalidConfiguration ArchiveState = "invalid_configuration"
	ArchiveUnreachable          ArchiveState = "unreachable"
	ArchiveUninitialised        ArchiveState = "uninitialised"
	ArchiveEmpty                ArchiveState = "empty"
	ArchivePopulated            ArchiveState = "populated"
	ArchiveDamaged              ArchiveState = "damaged"
	ArchiveUpgradeRequired      ArchiveState = "upgrade_required"
)

type ArchiveOperation string

const (
	ArchiveStatus     ArchiveOperation = "status"
	ArchiveInit       ArchiveOperation = "init"
	ArchiveSetDefault ArchiveOperation = "set_default"
	ArchiveUpload     ArchiveOperation = "upload"
	ArchiveDownload   ArchiveOperation = "download"
	ArchiveVerify     ArchiveOperation = "verify"
)

type WorkspaceState string

const (
	WorkspaceAbsent             WorkspaceState = "absent"
	WorkspaceCurrent            WorkspaceState = "current"
	WorkspaceBehind             WorkspaceState = "behind"
	WorkspaceDamaged            WorkspaceState = "damaged"
	WorkspaceArchiveMismatch    WorkspaceState = "archive_mismatch"
	WorkspaceInvalidDestination WorkspaceState = "invalid_destination"
	WorkspaceUpgradeRequired    WorkspaceState = "upgrade_required"
)

type WorkspaceOperation string

const (
	WorkspaceStatus     WorkspaceOperation = "status"
	WorkspaceSetDefault WorkspaceOperation = "set_default"
	WorkspaceDownload   WorkspaceOperation = "download"
	WorkspaceVerify     WorkspaceOperation = "verify"
	WorkspaceRepair     WorkspaceOperation = "repair"
	WorkspaceConflicts  WorkspaceOperation = "conflicts"
)

type Transition[S ~string] struct {
	Allowed    bool   `json:"allowed"`
	NextState  S      `json:"next_state"`
	NextAction Action `json:"next_action"`
}

func AllArchiveStates() []ArchiveState {
	return []ArchiveState{ArchiveInvalidAddress, ArchiveInvalidConfiguration, ArchiveUnreachable, ArchiveUninitialised, ArchiveEmpty, ArchivePopulated, ArchiveDamaged, ArchiveUpgradeRequired}
}

func AllArchiveOperations() []ArchiveOperation {
	return []ArchiveOperation{ArchiveStatus, ArchiveInit, ArchiveSetDefault, ArchiveUpload, ArchiveDownload, ArchiveVerify}
}

func AllWorkspaceStates() []WorkspaceState {
	return []WorkspaceState{WorkspaceAbsent, WorkspaceCurrent, WorkspaceBehind, WorkspaceDamaged, WorkspaceArchiveMismatch, WorkspaceInvalidDestination, WorkspaceUpgradeRequired}
}

func AllWorkspaceOperations() []WorkspaceOperation {
	return []WorkspaceOperation{WorkspaceStatus, WorkspaceSetDefault, WorkspaceDownload, WorkspaceVerify, WorkspaceRepair, WorkspaceConflicts}
}

func ArchiveTransition(state ArchiveState, operation ArchiveOperation) Transition[ArchiveState] {
	if operation == ArchiveStatus {
		return Transition[ArchiveState]{Allowed: true, NextState: state}
	}
	switch state {
	case ArchiveInvalidAddress, ArchiveInvalidConfiguration:
		return rejected(state, "archive", "status")
	case ArchiveUnreachable:
		return rejected(state, "archive", "status")
	case ArchiveUninitialised:
		if operation == ArchiveInit {
			return allowed(ArchiveEmpty)
		}
		return rejected(state, "archive", "init")
	case ArchiveEmpty:
		switch operation {
		case ArchiveSetDefault, ArchiveDownload, ArchiveVerify:
			return allowed(state)
		case ArchiveUpload:
			return allowed(ArchivePopulated)
		}
	case ArchivePopulated:
		switch operation {
		case ArchiveSetDefault, ArchiveDownload, ArchiveVerify, ArchiveUpload:
			return allowed(state)
		}
	case ArchiveDamaged:
		if operation == ArchiveVerify {
			return allowed(state)
		}
		return rejected(state, "archive", "verify", "--deep")
	case ArchiveUpgradeRequired:
		return rejected(state, "version", "--json")
	}
	return rejected(state, "archive", "status")
}

func WorkspaceTransition(state WorkspaceState, operation WorkspaceOperation) Transition[WorkspaceState] {
	if operation == WorkspaceStatus {
		return Transition[WorkspaceState]{Allowed: true, NextState: state}
	}
	switch state {
	case WorkspaceAbsent:
		switch operation {
		case WorkspaceSetDefault:
			return allowed(state)
		case WorkspaceDownload:
			return allowed(WorkspaceCurrent)
		default:
			return rejected(state, "archive", "download")
		}
	case WorkspaceCurrent:
		switch operation {
		case WorkspaceSetDefault, WorkspaceVerify, WorkspaceConflicts:
			return allowed(state)
		case WorkspaceDownload:
			return allowed(WorkspaceCurrent)
		}
	case WorkspaceBehind:
		switch operation {
		case WorkspaceSetDefault, WorkspaceVerify, WorkspaceConflicts:
			return allowed(state)
		case WorkspaceDownload:
			return allowed(WorkspaceCurrent)
		}
	case WorkspaceDamaged:
		switch operation {
		case WorkspaceSetDefault, WorkspaceVerify, WorkspaceConflicts:
			return allowed(state)
		case WorkspaceRepair:
			return allowed(WorkspaceCurrent)
		}
		return rejected(state, "workspace", "repair", "--backup")
	case WorkspaceArchiveMismatch:
		return rejected(state, "workspace", "status")
	case WorkspaceInvalidDestination:
		return rejected(state, "workspace", "status")
	case WorkspaceUpgradeRequired:
		return rejected(state, "version", "--json")
	}
	return rejected(state, "workspace", "status")
}

func allowed[S ~string](next S) Transition[S] {
	return Transition[S]{Allowed: true, NextState: next}
}

func rejected[S ~string](state S, args ...string) Transition[S] {
	return Transition[S]{NextState: state, NextAction: Action{Command: "aha", Args: args}}
}
