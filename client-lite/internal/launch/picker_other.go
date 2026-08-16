//go:build !windows

package launch

import "fmt"

type PickedApplication struct {
	Source    string `json:"source"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Arguments string `json:"arguments,omitempty"`
}

func pickApplicationFiles(string) ([]PickedApplication, error) {
	return nil, fmt.Errorf("文件选择仅支持 Windows")
}
