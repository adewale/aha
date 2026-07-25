package adapters

import (
	"reflect"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// versionSignalCases pins, per adapter, the kind of version the source carries
// and a fixture line that carries it. A source with no version anywhere states
// that as VersionKindNone rather than being absent from the table.
var versionSignalCases = []struct {
	source    string
	kind      model.VersionKind
	raw       string
	wantValue string
}{
	{
		source:    "claude-code",
		kind:      model.VersionKindProducer,
		raw:       `{"type":"user","uuid":"u1","version":"2.1.92","message":{"role":"user","content":"hi"}}`,
		wantValue: "2.1.92",
	},
	{
		source:    "codex",
		kind:      model.VersionKindProducer,
		raw:       `{"timestamp":"2026-03-27T09:21:37.000Z","type":"session_meta","payload":{"id":"s1","cli_version":"0.116.0"}}`,
		wantValue: "0.116.0",
	},
	{
		source:    "pi",
		kind:      model.VersionKindSchema,
		raw:       `{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z"}`,
		wantValue: "3",
	},
	{
		source:    "opencode",
		kind:      model.VersionKindNone,
		raw:       `{"type":"session","id":"ses_1","row":{"id":"ses_1","data":{"directory":"/w"}}}`,
		wantValue: "",
	},
}

func TestAdapterVersionSignalReadsTheDeclaredField(t *testing.T) {
	for _, tc := range versionSignalCases {
		t.Run(tc.source, func(t *testing.T) {
			adapter := Builtins()[tc.source]
			if adapter == nil {
				t.Fatalf("no builtin adapter %q", tc.source)
			}
			got := adapter.VersionSignal(model.ParsedEntry{RawJSON: tc.raw})
			if got.Kind() != tc.kind {
				t.Fatalf("kind=%q want %q", got.Kind(), tc.kind)
			}
			if got.Value() != tc.wantValue {
				t.Fatalf("value=%q want %q", got.Value(), tc.wantValue)
			}
		})
	}
}

// TestAdapterVersionSignalIsEmptyOnRecordsWithoutOne separates the two things
// a signal reports: the kind is a property of the source and never changes,
// the value is a property of the record and is usually absent — only session
// headers carry it.
func TestAdapterVersionSignalIsEmptyOnRecordsWithoutOne(t *testing.T) {
	const noVersion = `{"type":"message","id":"m1","message":{"role":"user","content":"no version here"}}`
	for _, tc := range versionSignalCases {
		t.Run(tc.source, func(t *testing.T) {
			got := Builtins()[tc.source].VersionSignal(model.ParsedEntry{RawJSON: noVersion})
			if got.Kind() != tc.kind {
				t.Fatalf("kind=%q want %q — the kind is a property of the source, not of the record", got.Kind(), tc.kind)
			}
			if got.Value() != "" {
				t.Fatalf("value=%q want empty for a record carrying no version", got.Value())
			}
		})
	}
}

// TestPiVersionIsSchemaNotProducer is the load-bearing assertion of the whole
// stratification. Pi's `version` is the on-disk format revision; it moves when
// the file format changes, not when Pi ships. Reporting it as a producer
// version would yield one bucket on the wrong axis and the false confidence of
// version coverage — worse than declaring Pi unstratified.
func TestPiVersionIsSchemaNotProducer(t *testing.T) {
	got := Pi{}.VersionSignal(model.ParsedEntry{RawJSON: `{"type":"session","version":3,"id":"s"}`})
	if got.Kind() == model.VersionKindProducer {
		t.Fatal("Pi's `version` is an on-disk schema revision; classifying it as a producer version stratifies coverage on the wrong axis")
	}
	if got.Kind() != model.VersionKindSchema {
		t.Fatalf("Pi kind=%q want %q", got.Kind(), model.VersionKindSchema)
	}
	// The schema revision is an integer in the file; it must not arrive as a
	// float rendering.
	if got.Value() != "3" {
		t.Fatalf("Pi schema version=%q want %q", got.Value(), "3")
	}
}

// TestEveryBuiltinDeclaresAKindFromTheClosedSet is the exhaustive check over
// Builtins() x VersionKind: a new adapter cannot land without its kind being
// one of the three, and the zero VersionSignal — the shape a forgotten
// implementation returns — is not one of them.
func TestEveryBuiltinDeclaresAKindFromTheClosedSet(t *testing.T) {
	closed := map[model.VersionKind]bool{}
	for _, k := range model.VersionKinds() {
		closed[k] = true
	}
	if len(closed) != 3 {
		t.Fatalf("VersionKinds() has %d members; the closed set is none/producer/schema", len(closed))
	}
	covered := map[model.VersionKind]int{}
	for name, adapter := range Builtins() {
		kind := adapter.VersionSignal(model.ParsedEntry{RawJSON: `{}`}).Kind()
		if !closed[kind] {
			t.Fatalf("adapter %q declares kind %q, which is outside the closed set %v", name, kind, model.VersionKinds())
		}
		covered[kind]++
	}
	for _, k := range model.VersionKinds() {
		if covered[k] == 0 {
			t.Fatalf("no builtin adapter declares kind %q; every kind must be exercised by a real adapter or removed from the set", k)
		}
	}
}

// TestZeroVersionSignalIsNotAValidKind documents the one invalid state Go
// leaves representable: every struct has a zero value, so VersionSignal{} can
// be written even though no constructor produces it. It must not pass for a
// declared kind, which is what makes the exhaustive check above catch a
// forgotten implementation.
func TestZeroVersionSignalIsNotAValidKind(t *testing.T) {
	var zero model.VersionSignal
	for _, k := range model.VersionKinds() {
		if zero.Kind() == k {
			t.Fatalf("the zero VersionSignal reads as declared kind %q; a forgotten implementation would pass the closed-set check", k)
		}
	}
}

// TestNoVersionCannotCarryAValue pins the construction-time guarantee: the
// constructors are the only way to build a signal, and the one for "this
// source has no version" takes no value to contradict itself with.
func TestNoVersionCannotCarryAValue(t *testing.T) {
	got := model.NoVersion()
	if got.Kind() != model.VersionKindNone {
		t.Fatalf("NoVersion().Kind()=%q want %q", got.Kind(), model.VersionKindNone)
	}
	if got.Value() != "" {
		t.Fatalf("NoVersion() carries value %q", got.Value())
	}
	ctor := reflect.TypeOf(model.NoVersion)
	if ctor.NumIn() != 0 {
		t.Fatalf("NoVersion takes %d argument(s); it must take none so VersionKindNone cannot be paired with a version", ctor.NumIn())
	}
}

// TestSourceAdapterInterfaceRequiresVersionSignal asserts the method is on the
// interface, not merely present on today's four adapters. Builtins() is typed
// as SourceAdapter, so an adapter that does not declare where its version
// lives cannot be registered.
func TestSourceAdapterInterfaceRequiresVersionSignal(t *testing.T) {
	iface := reflect.TypeOf((*SourceAdapter)(nil)).Elem()
	method, ok := iface.MethodByName("VersionSignal")
	if !ok {
		t.Fatal("SourceAdapter does not require VersionSignal; a new adapter could omit its version declaration and still compile")
	}
	if got, want := method.Type.NumIn(), 1; got != want {
		t.Fatalf("VersionSignal takes %d parameter(s), want %d (model.ParsedEntry)", got, want)
	}
	if got, want := method.Type.In(0), reflect.TypeOf(model.ParsedEntry{}); got != want {
		t.Fatalf("VersionSignal parameter is %v, want %v", got, want)
	}
	if got, want := method.Type.Out(0), reflect.TypeOf(model.VersionSignal{}); got != want {
		t.Fatalf("VersionSignal returns %v, want %v", got, want)
	}
}
