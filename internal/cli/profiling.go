package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
)

type profileOptions struct {
	CPUProfile string
	MemProfile string
}

func profileOptionsFromArgs(args []string) ([]string, profileOptions, error) {
	opts := profileOptions{CPUProfile: os.Getenv("AHA_CPU_PROFILE"), MemProfile: os.Getenv("AHA_MEM_PROFILE")}
	clean := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			clean = append(clean, args[i:]...)
			break
		}
		switch {
		case arg == "--cpuprofile" || arg == "--memprofile":
			if i+1 >= len(args) {
				return nil, opts, fmt.Errorf("%s requires path", arg)
			}
			value := args[i+1]
			if value == "" || strings.HasPrefix(value, "--") {
				return nil, opts, fmt.Errorf("%s requires path", arg)
			}
			if arg == "--cpuprofile" {
				opts.CPUProfile = value
			} else {
				opts.MemProfile = value
			}
			i++
		case strings.HasPrefix(arg, "--cpuprofile="):
			opts.CPUProfile = strings.TrimPrefix(arg, "--cpuprofile=")
			if opts.CPUProfile == "" {
				return nil, opts, fmt.Errorf("--cpuprofile requires path")
			}
		case strings.HasPrefix(arg, "--memprofile="):
			opts.MemProfile = strings.TrimPrefix(arg, "--memprofile=")
			if opts.MemProfile == "" {
				return nil, opts, fmt.Errorf("--memprofile requires path")
			}
		default:
			clean = append(clean, arg)
		}
	}
	return clean, opts, nil
}

func runWithProfiling(opts profileOptions, fn func() error) error {
	stop, err := startProfiling(opts)
	if err != nil {
		return err
	}
	runErr := fn()
	stopErr := stop()
	if runErr != nil {
		return runErr
	}
	return stopErr
}

func startProfiling(opts profileOptions) (func() error, error) {
	var cpuFile *os.File
	if opts.CPUProfile != "" {
		f, err := createProfileFile(opts.CPUProfile)
		if err != nil {
			return nil, err
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			_ = f.Close()
			return nil, err
		}
		cpuFile = f
	}
	return func() error {
		var firstErr error
		if cpuFile != nil {
			pprof.StopCPUProfile()
			if err := cpuFile.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if opts.MemProfile != "" {
			runtime.GC()
			f, err := createProfileFile(opts.MemProfile)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return firstErr
			}
			if err := pprof.WriteHeapProfile(f); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}, nil
}

func createProfileFile(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.Create(path)
}
