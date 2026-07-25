package model

// VersionKind classifies what a source's version signal actually measures.
// The distinction is load-bearing rather than descriptive: stratifying
// key-path coverage by a schema revision while labelling it a producer
// version yields one bucket on the wrong axis and the false confidence of
// version coverage, which is worse than declaring the source unstratified.
type VersionKind string

const (
	// VersionKindNone declares that a source carries no version anywhere in
	// its on-disk shape. It is a reviewable claim, not an omission: coverage
	// for such a source is known to be unstratified.
	VersionKindNone VersionKind = "none"
	// VersionKindProducer is the version of the agent that wrote the file,
	// which is what moves when a release changes the on-disk shape. Claude
	// Code writes it as `version`, Codex as `payload.cli_version`.
	VersionKindProducer VersionKind = "producer"
	// VersionKindSchema is the revision of the on-disk format itself, which
	// moves independently of — and far more slowly than — the producer. Pi
	// writes it as `version`, an integer.
	VersionKindSchema VersionKind = "schema"
)

// VersionKinds returns the closed set of kinds, in declaration order.
// Exhaustive tests iterate this rather than a list of their own, so adding a
// kind forces every such test to account for it.
func VersionKinds() []VersionKind {
	return []VersionKind{VersionKindNone, VersionKindProducer, VersionKindSchema}
}

// VersionSignal is where a source records its version and what that version
// measures.
//
// Its fields are unexported and the three constructors below are its only
// exported constructors, so a signal cannot be built carrying a kind outside
// the closed set, and VersionKindNone cannot be paired with a version value
// that contradicts it. The one invalid state Go leaves representable is the
// zero value, which every struct has: its kind is not a member of
// VersionKinds(), so the exhaustive check over the adapter registry catches a
// forgotten implementation rather than reading it as "no version".
type VersionSignal struct {
	kind  VersionKind
	value string
}

// NoVersion declares that the source carries no version. It takes no value
// because there is none to record.
func NoVersion() VersionSignal {
	return VersionSignal{kind: VersionKindNone}
}

// ProducerVersion records the version of the agent that wrote a record. An
// empty value is normal and means this record carries none: the kind is a
// property of the source, the value a property of the record, and typically
// only a session header carries it.
func ProducerVersion(value string) VersionSignal {
	return VersionSignal{kind: VersionKindProducer, value: value}
}

// SchemaVersion records the on-disk format revision a record was written
// against. An empty value means this record carries none.
func SchemaVersion(value string) VersionSignal {
	return VersionSignal{kind: VersionKindSchema, value: value}
}

// Kind reports what the signal measures. It is returned verbatim, so the zero
// VersionSignal reports an empty kind and fails a closed-set check loudly
// instead of masquerading as VersionKindNone.
func (s VersionSignal) Kind() VersionKind { return s.kind }

// Value reports the version as the producer wrote it, or "" when the record
// carries none.
func (s VersionSignal) Value() string { return s.value }
