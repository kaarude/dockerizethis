package main

import (
	"io"
	"os"
	"os/exec"
)

// test -t checks the actual descriptor, unlike ModeCharDevice which includes /dev/null.
// If the platform has no test utility, commands remain noninteractive.
func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	check := exec.Command("test", "-t", "1")
	check.Stdout = file
	return check.Run() == nil
}
