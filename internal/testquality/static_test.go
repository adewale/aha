package testquality_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNoFocusedOrSleepBasedTests(t *testing.T) {
	root := filepath.Join("..", "..")
	patterns := []string{"time.Sleep(", "test.only", "fit(", "fdescribe("}
	var offenders []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || strings.HasSuffix(filepath.ToSlash(path), "internal/testquality/static_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, pattern := range patterns {
				if strings.Contains(line, pattern) {
					offenders = append(offenders, filepath.ToSlash(path)+":"+strconv.Itoa(i+1)+":"+pattern)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(offenders) > 0 {
		t.Fatalf("tests must not be focused or sleep-based; offenders: %s", strings.Join(offenders, ", "))
	}
}

func TestNoLoggingInsteadOfAssertions(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") || strings.HasSuffix(filepath.ToSlash(path), "internal/testquality/static_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "t.Log(") || strings.Contains(line, "t.Logf(") {
				offenders = append(offenders, filepath.ToSlash(path)+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(offenders) > 0 {
		t.Fatalf("tests must assert instead of only logging; offenders: %s", strings.Join(offenders, ", "))
	}
}
