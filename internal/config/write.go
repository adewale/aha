package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/paths"
	"github.com/tailscale/hujson"
)

func Write(path string, cfg model.Config, header string) (string, error) {
	if path == "" {
		path = DefaultPath()
	}
	expanded, err := paths.Expand(path)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(expanded)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if cfg.Schema == "" {
		cfg.Schema = SchemaV1
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	if header == "" {
		header = "// aha config (JSONC)\n"
	}
	content := append([]byte(header), append(b, '\n')...)

	mode := os.FileMode(0o600)
	if info, statErr := os.Lstat(expanded); statErr == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
		if previous, readErr := os.ReadFile(expanded); readErr == nil {
			if merged := mergeConfigJSONC(previous, b); merged != nil {
				content = merged
			}
		}
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return "", statErr
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, expanded); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return expanded, nil
}

// mergeConfigJSONC replaces known values while preserving the operator's
// comments and any namespaced extension members already represented in cfg.
func mergeConfigJSONC(previous, replacement []byte) []byte {
	oldValue, err := hujson.Parse(previous)
	if err != nil {
		return nil
	}
	newValue, err := hujson.Parse(replacement)
	if err != nil {
		return nil
	}
	oldObject, ok := oldValue.Value.(*hujson.Object)
	if !ok {
		return nil
	}
	newObject, ok := newValue.Value.(*hujson.Object)
	if !ok {
		return nil
	}
	newNames := make(map[string]bool, len(newObject.Members))
	for _, member := range newObject.Members {
		if name, ok := objectMemberName(member); ok {
			newNames[name] = true
		}
	}
	knownNames := configJSONFieldNames()
	kept := oldObject.Members[:0]
	for _, member := range oldObject.Members {
		name, ok := objectMemberName(member)
		if ok && knownNames[name] && !newNames[name] {
			continue
		}
		kept = append(kept, member)
	}
	oldObject.Members = kept

	positions := make(map[string]int, len(oldObject.Members))
	for i := range oldObject.Members {
		if name, ok := objectMemberName(oldObject.Members[i]); ok {
			positions[name] = i
		}
	}
	for _, member := range newObject.Members {
		name, ok := objectMemberName(member)
		if !ok {
			return nil
		}
		if i, exists := positions[name]; exists {
			if equalJSONValue(oldObject.Members[i].Value, member.Value) {
				continue
			}
			before := oldObject.Members[i].Value.BeforeExtra
			after := oldObject.Members[i].Value.AfterExtra
			oldObject.Members[i].Value = member.Value.Clone()
			oldObject.Members[i].Value.BeforeExtra = before
			oldObject.Members[i].Value.AfterExtra = after
			continue
		}
		oldObject.Members = append(oldObject.Members, hujson.ObjectMember{Name: member.Name.Clone(), Value: member.Value.Clone()})
	}
	oldValue.Format()
	return append(oldValue.Pack(), '\n')
}

func equalJSONValue(a, b hujson.Value) bool {
	compact := func(value hujson.Value) []byte {
		value.Standardize()
		var out bytes.Buffer
		if err := json.Compact(&out, value.Pack()); err != nil {
			return nil
		}
		return out.Bytes()
	}
	return bytes.Equal(compact(a.Clone()), compact(b.Clone()))
}

func configJSONFieldNames() map[string]bool {
	t := reflect.TypeOf(model.Config{})
	out := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func objectMemberName(member hujson.ObjectMember) (string, bool) {
	literal, ok := member.Name.Value.(hujson.Literal)
	if !ok {
		return "", false
	}
	return literal.String(), true
}
