package cli_test

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/cli"
)

func TestV02RegistryIsTheOnlyPublicCommandSurface(t *testing.T) {
	want := []string{"analyse", "archive", "dashboard", "init", "mcp", "search", "show", "status", "workspace"}
	got := cli.CommandNames()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands=%v want %v", got, want)
	}

	for _, old := range []string{"conflicts", "corpus", "depot", "doctor", "export", "incidents", "ingest", "read", "refresh", "serve", "snapshot", "verify"} {
		err := cli.Run([]string{old}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("legacy command %q err=%v, want unknown command", old, err)
		}
	}
}

func TestVersionJSONIdentifiesBuild(t *testing.T) {
	var out bytes.Buffer
	if err := cli.Run([]string{"version", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		BuiltAt string `json:"built_at"`
		Dirty   bool   `json:"dirty"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("version JSON: %v\n%s", err, out.String())
	}
	if got.Version != "0.2.0" || got.Commit == "" || got.BuiltAt == "" {
		t.Fatalf("version identity=%+v", got)
	}
}
