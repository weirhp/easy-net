//go:build windows

package clashsub

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

type failingProcessControl struct{}

func (failingProcessControl) Terminate() error { return errors.New("Access is denied") }
func (failingProcessControl) Close()           {}

func TestRetainedProcessControlTerminatesChild(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/d", "/c", "ping -t 127.0.0.1 >nul")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	executable := cmd.Path
	control, err := retainProcessControl(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	defer control.Close()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	if err := control.Terminate(); err != nil {
		t.Fatalf("terminate retained process: %v", err)
	}
	if err := waitOwnedProcessExit(cmd.Process.Pid, executable, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("os/exec did not observe the terminated child")
	}
}

func TestStopOwnedDoesNotReportObsoleteFirstTerminationError(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/d", "/c", "ping -t 127.0.0.1 >nul")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	runner := &mihomoRunner{
		configDir: t.TempDir(),
		commands: map[string]*managedMihomo{
			"test": {cmd: cmd, control: failingProcessControl{}},
		},
	}

	if err := runner.stopOwned("test", cmd.Path); err != nil {
		t.Fatalf("fallback terminated the process, so the first error must not escape: %v", err)
	}
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("fallback did not terminate the child")
	}
}
