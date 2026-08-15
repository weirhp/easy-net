//go:build !windows

package launch

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
