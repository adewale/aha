package cli

import "errors"

func requireAtMostOneOutputMode(modes ...bool) error {
	count := 0
	for _, mode := range modes {
		if mode {
			count++
		}
	}
	if count > 1 {
		return errors.New("mutually exclusive output modes")
	}
	return nil
}
