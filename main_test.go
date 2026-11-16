package typst_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"go.uber.org/goleak"
)

// The test binary doubles as a fake typst: run with "compile" or "--version" as its first
// argument it acts out what the source file asks for instead of running the tests, so the
// compiler's process handling is tested without a real typst and in milliseconds.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && (os.Args[1] == "compile" || os.Args[1] == "--version") {
		// syscall.Exit skips the race detector's exit hook, which would hold every fake for a second.
		syscall.Exit(fakeTypst(os.Args[1:]))
	}
	goleak.VerifyTestMain(m)
}

// fakeReport is what the fake writes to stdout for a source of "echo".
type fakeReport struct {
	Arguments   []string          `json:"arguments"`
	Environment []string          `json:"environment"`
	Files       map[string]string `json:"files"`
}

// The sources the fake understands.
const (
	fakeEcho        = "echo"   // write a fakeReport
	fakeFail        = "fail"   // diagnostics on stderr, exit 1
	fakeHang        = "hang"   // block until killed
	fakeEmpty       = "empty"  // exit 0 without output
	fakeFlood       = "flood"  // 200 KiB on stderr, exit 1
	fakeExit        = "exit 3" // exit 3 without diagnostics
	fakeKill        = "kill"   // diagnostics on stderr, then kill itself (SIGKILL on Unix)
	fakeLargePrefix = "large " // "large <n>": n bytes on stdout
	fakeWarn        = "warn"   // a warning on stderr, output, exit 0
	fakeLock        = "lock"   // make the render directory unremovable, output, exit 0; "lock fail" exits 1
)

func fakeTypst(arguments []string) int {
	if arguments[0] == "--version" {
		// The binary's name selects the version: a link named typst-<version> reports it.
		name := filepath.Base(os.Args[0])
		if version, found := strings.CutPrefix(name, "typst-"); found {
			fmt.Printf("typst %s (fake)\n", version)
			return 0
		}
		fmt.Println("typst 0.15.1 (fake)")
		return 0
	}
	source, err := os.ReadFile("main.typ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: read main.typ:", err)
		return 1
	}
	command := strings.TrimSpace(string(source))
	switch {
	case command == fakeFail:
		fmt.Fprintln(os.Stderr, "main.typ:1:1: warning: something minor")
		fmt.Fprintln(os.Stderr, "main.typ:2:5: error: unknown variable: nope")
		return 1
	case command == fakeHang:
		reader, _, _ := os.Pipe()
		_, _ = reader.Read(make([]byte, 1)) // blocks: nobody writes to the pipe
		return 0
	case command == fakeEmpty:
		return 0
	case command == fakeFlood:
		fmt.Fprint(os.Stderr, strings.Repeat("e", 200<<10))
		return 1
	case command == fakeExit:
		return 3
	case command == fakeKill:
		fmt.Fprintln(os.Stderr, "main.typ:1:1: warning: something minor")
		self, _ := os.FindProcess(os.Getpid())
		_ = self.Kill()
		select {} // the kill ends the process
	case command == fakeWarn:
		fmt.Fprintln(os.Stderr, "main.typ:1:16: warning: unknown font family: inter")
		fmt.Print("output")
		return 0
	case strings.HasPrefix(command, fakeLock):
		// Without write permission on the render directory, its project directory cannot go.
		if err := os.Chmod("..", 0o500); err != nil { //nolint:gosec // G302: the fake locks its render directory
			fmt.Fprintln(os.Stderr, "error:", err)
			return 2
		}
		if command != fakeLock {
			fmt.Fprintln(os.Stderr, "main.typ:1:1: error: locked")
			return 1
		}
		fmt.Print("output")
		return 0
	case strings.HasPrefix(command, fakeLargePrefix):
		size, _ := strconv.Atoi(strings.TrimPrefix(command, fakeLargePrefix))
		fmt.Print(strings.Repeat("x", size))
		return 0
	}
	report := fakeReport{Arguments: arguments, Environment: os.Environ(), Files: map[string]string{}}
	err = filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path) //nolint:gosec // G304: the fake reads the render directory it runs in
		report.Files[filepath.ToSlash(path)] = string(content)
		return err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return 1
	}
	return 0
}
