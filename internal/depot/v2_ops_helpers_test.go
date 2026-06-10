package depot_test

// opsSnapshot returns a copy of the fake's request counts ("METHOD key" ->
// count) for operation-invariant assertions in the v2 tests.
func (f *fakeS3) opsSnapshot() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.counts))
	for k, v := range f.counts {
		out[k] = v
	}
	return out
}
