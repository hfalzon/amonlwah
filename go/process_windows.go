package lhaw

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// BWS is a child of Amon Hen; cancel the entire provider process tree.
func configureCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		killer := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe"), "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
		if err := killer.Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
