//go:build windows

package launch

import "testing"

func TestPickApplicationFilesRejectsInvalidKind(t *testing.T) {
	if _, err := pickApplicationFiles("document"); err == nil {
		t.Fatal("invalid picker kind was accepted")
	}
}
