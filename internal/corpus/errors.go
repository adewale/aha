package corpus

import "fmt"

type NotFoundError struct {
	Kind  string
	Value string
}

func (e NotFoundError) Error() string { return fmt.Sprintf("%s not found: %s", e.Kind, e.Value) }

type AmbiguousError struct {
	Kind  string
	Value string
}

func (e AmbiguousError) Error() string { return fmt.Sprintf("ambiguous %s %q", e.Kind, e.Value) }
