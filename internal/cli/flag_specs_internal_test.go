package cli

import (
	"io"
	"testing"
)

func TestSpecBackedFlagsParseAfterPositionals(t *testing.T) {
	for _, tt := range []struct {
		command string
		specs   []FlagSpec
	}{
		{command: "search", specs: searchFlagSpecs},
		{command: "show", specs: showFlagSpecs},
	} {
		for _, spec := range tt.specs {
			if !spec.PostPosition {
				continue
			}
			t.Run(tt.command+"/"+spec.Name, func(t *testing.T) {
				args := []string{"positional", "--" + spec.Name}
				if spec.Kind != flagBool {
					args = append(args, valueForSpec(spec))
				}
				pf, err := parseFlagSpecs(tt.command, reorderArgsBySpec(args, tt.specs), io.Discard, tt.specs)
				if err != nil {
					t.Fatalf("parseFlagSpecs: %v", err)
				}
				if pf.NArg() != 1 || pf.Arg(0) != "positional" {
					t.Fatalf("positional args=%v, want [positional]", pf.Args())
				}
				switch spec.Kind {
				case flagBool:
					if !pf.Bool(spec.Name) {
						t.Fatalf("--%s did not parse true after positional", spec.Name)
					}
				case flagString:
					if pf.String(spec.Name) != valueForSpec(spec) {
						t.Fatalf("--%s=%q want %q", spec.Name, pf.String(spec.Name), valueForSpec(spec))
					}
				case flagInt:
					if pf.Int(spec.Name) != 7 {
						t.Fatalf("--%s=%d want 7", spec.Name, pf.Int(spec.Name))
					}
				}
			})
		}
	}
}

func valueForSpec(spec FlagSpec) string {
	if spec.Kind == flagInt {
		return "7"
	}
	return "value"
}
