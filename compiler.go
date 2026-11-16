package typst

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Defaults of Options.
const (
	DefaultBinary            = "typst"
	DefaultTimeout           = 30 * time.Second
	DefaultMaximumOutputSize = 64 << 20 // 64 MiB
)

// MinimumVersion is the oldest typst release New accepts: the command-line flags Compile passes
// exist since then.
const MinimumVersion = "0.13.0"

const (
	// maximumDiagnosticsSize caps what Compile keeps of the process's stderr.
	maximumDiagnosticsSize = 64 << 10
	// versionTimeout bounds the version check in New.
	versionTimeout = 10 * time.Second
	// waitDelay bounds how long Compile waits for the output pipes after the process was killed.
	waitDelay = 5 * time.Second
	// blockedProxy is a proxy no connection reaches, so typst cannot download a package.
	blockedProxy = "http://127.0.0.1:0"
)

// environment is the whole environment of the typst process: no TYPST_* variable of the
// service's own environment changes a render, and every download goes to blockedProxy.
var environment = []string{
	"HTTP_PROXY=" + blockedProxy,
	"HTTPS_PROXY=" + blockedProxy,
	"ALL_PROXY=" + blockedProxy,
	"http_proxy=" + blockedProxy,
	"https_proxy=" + blockedProxy,
	"all_proxy=" + blockedProxy,
}

var (
	// versionPattern reads the version from the output of typst --version.
	versionPattern = regexp.MustCompile(`^typst (\d+)\.(\d+)\.(\d+)`)
	// requiredVersionPattern is the form of Options.Version.
	requiredVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Options configures a Compiler.
type Options struct {
	// Binary is the typst executable, a path or a name looked up in PATH; empty means
	// DefaultBinary. New resolves it to an absolute path once.
	Binary string
	// Version, when set, is the exact typst version ("0.15.1") the binary must report, so a
	// binary that drifted from the one the templates were written for fails at startup. Empty
	// accepts any version from MinimumVersion on.
	Version string
	// FontPaths are directories typst searches for fonts, recursively. They must exist.
	FontPaths []string
	// SystemFonts lets typst use the fonts installed on the host besides FontPaths and its
	// embedded fonts. Off by default, so a render looks the same on every host.
	SystemFonts bool
	// PackagePath is a directory of vendored packages, laid out as
	// <namespace>/<name>/<version>, for #import "@preview/..." and "@local/...". Empty means
	// none. Packages are never downloaded at render time.
	PackagePath string
	// TemporaryDirectory is where each compile creates its render directory; empty means
	// os.TempDir(). It must exist.
	TemporaryDirectory string
	// Timeout bounds one compile, which is killed when it runs longer; zero means DefaultTimeout.
	Timeout time.Duration
	// MaximumOutputSize bounds the bytes one compile may produce; zero means
	// DefaultMaximumOutputSize.
	MaximumOutputSize int
	// Observers are told about every compile; nil reports to none. Started in order, finished
	// in reverse order.
	Observers []Observer
}

// Compiler runs typst. It is safe for concurrent use; every Compile runs its own process in its
// own directory.
type Compiler struct {
	options Options
	version string
}

// New resolves the binary, runs it once with --version and returns a Compiler. It fails when the
// binary is missing, reports a version below MinimumVersion or other than Options.Version, or
// when a font, package or temporary directory does not exist.
func New(options *Options) (*Compiler, error) {
	if options == nil {
		options = &Options{}
	}
	compiler := &Compiler{options: *options}
	compiler.options.FontPaths = slices.Clone(options.FontPaths)
	compiler.options.Observers = slices.Clone(options.Observers)
	if err := compiler.applyDefaults(); err != nil {
		return nil, err
	}
	version, err := compiler.checkVersion()
	if err != nil {
		return nil, err
	}
	compiler.version = version
	return compiler, nil
}

func (c *Compiler) applyDefaults() error {
	options := &c.options
	if options.Timeout < 0 || options.MaximumOutputSize < 0 {
		return errors.New("typst: Timeout and MaximumOutputSize must not be negative")
	}
	if options.Timeout == 0 {
		options.Timeout = DefaultTimeout
	}
	if options.MaximumOutputSize == 0 {
		options.MaximumOutputSize = DefaultMaximumOutputSize
	}
	if slices.Contains(options.Observers, nil) {
		return errors.New("typst: Observers holds a nil Observer")
	}
	if options.Version != "" && !requiredVersionPattern.MatchString(options.Version) {
		return fmt.Errorf("typst: Version %q is not major.minor.patch", options.Version)
	}
	binary, err := resolveBinary(options.Binary)
	if err != nil {
		return err
	}
	options.Binary = binary
	for index, path := range options.FontPaths {
		// typst takes every font path in one --font-path value, joined by the list separator.
		if strings.ContainsRune(path, filepath.ListSeparator) {
			return fmt.Errorf("typst: font path %q must be one directory", path)
		}
		if options.FontPaths[index], err = existingDirectory(path, "font path"); err != nil {
			return err
		}
	}
	if options.PackagePath != "" {
		if options.PackagePath, err = existingDirectory(options.PackagePath, "package path"); err != nil {
			return err
		}
	}
	if options.TemporaryDirectory != "" {
		if options.TemporaryDirectory, err = existingDirectory(options.TemporaryDirectory, "temporary directory"); err != nil {
			return err
		}
	}
	return nil
}

func resolveBinary(binary string) (string, error) {
	if binary == "" {
		binary = DefaultBinary
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("typst: find the binary %q: %w", binary, err)
	}
	// The process runs in the render directory, so a relative path would no longer resolve.
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("typst: resolve the binary %q: %w", path, err)
	}
	return absolute, nil
}

// existingDirectory returns the absolute path of a directory that must exist. typst ignores a
// missing font directory silently, so the mistake surfaces here instead of as a wrong font.
func existingDirectory(path, what string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("typst: %s must not be empty", what)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("typst: resolve the %s %q: %w", what, path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("typst: %s: %w", what, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("typst: %s %q is not a directory", what, absolute)
	}
	return absolute, nil
}

// checkVersion runs the binary with --version and returns its major.minor.patch.
func (c *Compiler) checkVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, c.options.Binary, "--version") //nolint:gosec // G204: the binary is the application's own setting
	command.Env = environment
	command.WaitDelay = waitDelay
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("typst: run %s --version: %w", c.options.Binary, err)
	}
	version, err := parseVersion(string(output))
	if err != nil {
		return "", err
	}
	if c.options.Version != "" && version != c.options.Version {
		return "", fmt.Errorf("typst: %s is version %s, the options require %s", c.options.Binary, version, c.options.Version)
	}
	if compareVersions(version, MinimumVersion) < 0 {
		return "", fmt.Errorf("typst: %s is version %s, older than the minimum %s", c.options.Binary, version, MinimumVersion)
	}
	return version, nil
}

func parseVersion(output string) (string, error) {
	match := versionPattern.FindStringSubmatch(strings.TrimSpace(output))
	if match == nil {
		return "", fmt.Errorf("typst: unexpected --version output %q", strings.TrimSpace(output))
	}
	return match[1] + "." + match[2] + "." + match[3], nil
}

// compareVersions compares two major.minor.patch versions the way cmp.Compare does.
func compareVersions(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := range leftParts {
		leftNumber, _ := strconv.Atoi(leftParts[index])   // both versions matched versionPattern
		rightNumber, _ := strconv.Atoi(rightParts[index]) // both versions matched versionPattern
		if leftNumber != rightNumber {
			return leftNumber - rightNumber
		}
	}
	return 0
}

// Version returns the major.minor.patch version of the binary New checked.
func (c *Compiler) Version() string {
	return c.version
}

// Compile renders the document and returns the PDF, SVG or PNG bytes. It returns
// ErrInvalidDocument before running anything for a document it cannot compile, a *CompileError
// when typst refuses the template, ErrTimeout, ErrOutputTooLarge, ErrPageNotFound, or the
// context's error wrapped when ctx ends first. The render directory is removed before Compile
// returns; when that fails, the error says so, joined with the compile's own error.
func (c *Compiler) Compile(ctx context.Context, document *Document) ([]byte, error) {
	if err := document.validate(); err != nil {
		return nil, err
	}
	data, err := document.encodeData()
	if err != nil {
		return nil, err
	}
	compile := &Compile{Name: document.Name, Format: document.format()}
	ctx = c.started(ctx, compile)
	started := time.Now()
	output, warnings, err := c.render(ctx, document, data)
	c.finished(ctx, compile, &Outcome{
		Result: resultOf(err, ctx.Err() != nil), Duration: time.Since(started), OutputSize: len(output), Warnings: warnings, Err: err,
	})
	return output, err
}

func (c *Compiler) started(ctx context.Context, compile *Compile) context.Context {
	for _, observer := range c.options.Observers {
		ctx = observer.CompileStarted(ctx, compile) //nolint:fatcontext // each observer wraps the previous one's context once, bounded by the observers
	}
	return ctx
}

func (c *Compiler) finished(ctx context.Context, compile *Compile, outcome *Outcome) {
	for index := len(c.options.Observers) - 1; index >= 0; index-- {
		c.options.Observers[index].CompileFinished(ctx, compile, outcome)
	}
}

// render runs one compile in a fresh directory: <directory>/project is the project root, the
// rest of <directory> holds what typst needs outside the root.
// It returns the output and the warnings typst printed for a successful compile.
func (c *Compiler) render(ctx context.Context, document *Document, data []byte) (output []byte, warnings string, err error) {
	directory, err := os.MkdirTemp(c.options.TemporaryDirectory, "typst-")
	if err != nil {
		return nil, "", fmt.Errorf("typst: create the render directory: %w", err)
	}
	defer func() {
		// The directory holds the caller's data, so leaving it behind is an error, not a leak to ignore.
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			output, warnings = nil, ""
			err = errors.Join(err, fmt.Errorf("typst: remove the render directory: %w", removeErr))
		}
	}()
	project := filepath.Join(directory, "project")
	if err := writeProject(project, document, data); err != nil {
		return nil, "", err
	}
	output, warnings, err = c.run(ctx, project, c.arguments(document, directory))
	if err == nil && len(output) == 0 {
		if document.format() != FormatPDF {
			// typst exits 0 without output when --pages names no page of the document.
			return nil, "", ErrPageNotFound
		}
		return nil, "", errors.New("typst: the compile produced no output")
	}
	return output, warnings, err
}

// writeProject writes the template, the data and the files with permissions for the owner only.
func writeProject(project string, document *Document, data []byte) error {
	files := map[string][]byte{SourceFile: document.Source}
	if data != nil {
		files[DataFile] = data
	}
	for name, content := range document.Files {
		files[name] = content
	}
	for name, content := range files {
		path := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("typst: create the directory of %s: %w", name, err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return fmt.Errorf("typst: write %s: %w", name, err)
		}
	}
	return nil
}

// arguments builds the command line of one compile. Nothing of the document's content or data
// is on it, only the options and the document's format settings.
func (c *Compiler) arguments(document *Document, directory string) []string {
	packagePath := c.options.PackagePath
	if packagePath == "" {
		packagePath = filepath.Join(directory, "packages")
	}
	format := document.format()
	arguments := []string{
		"compile",
		"--root", ".",
		"--diagnostic-format", "short",
		"--format", string(format),
		"--package-path", packagePath,
		"--package-cache-path", filepath.Join(directory, "cache"),
	}
	if !c.options.SystemFonts {
		arguments = append(arguments, "--ignore-system-fonts")
	}
	if len(c.options.FontPaths) > 0 {
		arguments = append(arguments, "--font-path", strings.Join(c.options.FontPaths, string(filepath.ListSeparator)))
	}
	if format != FormatPDF {
		arguments = append(arguments, "--pages", strconv.Itoa(max(document.Page, 1)))
	}
	if document.PixelsPerInch > 0 {
		arguments = append(arguments, "--ppi", strconv.Itoa(document.PixelsPerInch))
	}
	return append(arguments, SourceFile, "-")
}

// run starts typst in the project directory and reads its output from stdout; on success it
// returns what typst printed on stderr as the warnings.
func (c *Compiler) run(ctx context.Context, project string, arguments []string) ([]byte, string, error) {
	runContext, cancelTimeout := context.WithTimeoutCause(ctx, c.options.Timeout, ErrTimeout)
	defer cancelTimeout()
	runContext, cancel := context.WithCancelCause(runContext)
	defer cancel(nil)

	stdout := &cappedBuffer{limit: c.options.MaximumOutputSize, exceeded: func() { cancel(ErrOutputTooLarge) }}
	stderr := &cappedBuffer{limit: maximumDiagnosticsSize, truncate: true}
	//nolint:gosec // G204: the binary is the application's setting; the arguments carry options and paths, never document content
	command := exec.CommandContext(runContext, c.options.Binary, arguments...)
	command.Dir = project
	command.Env = environment
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = waitDelay

	err := command.Run()
	if err == nil {
		return stdout.content, stderr.String(), nil
	}
	switch cause := context.Cause(runContext); {
	case ctx.Err() != nil:
		// The context's error, not its cause: a cause is the caller's own error, which would
		// make this compile look like whatever failed elsewhere.
		return nil, "", fmt.Errorf("typst: compile: %w", ctx.Err())
	case errors.Is(cause, ErrOutputTooLarge), errors.Is(cause, ErrTimeout):
		return nil, "", cause
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return nil, "", &CompileError{ExitCode: exitError.ExitCode(), Diagnostics: stderr.String()}
	}
	return nil, "", fmt.Errorf("typst: run %s: %w", c.options.Binary, err)
}

// cappedBuffer collects a process's output up to limit bytes. Beyond the limit it either drops
// the rest silently (truncate) or fails the write and calls exceeded, which kills the process.
type cappedBuffer struct {
	content  []byte
	limit    int
	truncate bool
	exceeded func()
}

func (b *cappedBuffer) Write(chunk []byte) (int, error) {
	free := b.limit - len(b.content)
	if len(chunk) <= free {
		b.content = append(b.content, chunk...)
		return len(chunk), nil
	}
	if b.truncate {
		b.content = append(b.content, chunk[:free]...)
		return len(chunk), nil
	}
	b.exceeded()
	return 0, ErrOutputTooLarge
}

func (b *cappedBuffer) String() string {
	return strings.ToValidUTF8(string(b.content), "\uFFFD")
}
