package typst_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/govengers/typst"
)

// fakeBinary returns the path of the test binary under a name whose --version reports version.
func fakeBinary(t *testing.T, version string) string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	link := filepath.Join(t.TempDir(), "typst-"+version)
	require.NoError(t, os.Symlink(executable, link))
	return link
}

func newCompiler(t *testing.T, options *typst.Options) *typst.Compiler {
	t.Helper()
	if options.Binary == "" {
		options.Binary = fakeBinary(t, "0.15.1")
	}
	compiler, err := typst.New(options)
	require.NoError(t, err)
	return compiler
}

func echo(t *testing.T, compiler *typst.Compiler, document *typst.Document) fakeReport {
	t.Helper()
	output, err := compiler.Compile(t.Context(), document)
	require.NoError(t, err)
	var report fakeReport
	require.NoError(t, json.Unmarshal(output, &report))
	return report
}

func TestNewChecksTheBinary(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	cases := []struct {
		name    string
		options typst.Options
		want    string
	}{
		{"missing binary", typst.Options{Binary: filepath.Join(t.TempDir(), "typst")}, "find the binary"},
		{"too old", typst.Options{Binary: fakeBinary(t, "0.12.0")}, "older than the minimum 0.13.0"},
		{"other than required", typst.Options{Binary: fakeBinary(t, "0.15.0"), Version: "0.15.1"}, "the options require 0.15.1"},
		{"malformed required version", typst.Options{Binary: fakeBinary(t, "0.15.1"), Version: "0.15"}, "is not major.minor.patch"},
		{"required version with a suffix", typst.Options{Binary: fakeBinary(t, "0.15.1"), Version: "0.15.1-rc1"}, "is not major.minor.patch"},
		{"unparseable version", typst.Options{Binary: fakeBinary(t, "garbage")}, "unexpected --version output"},
		{"negative timeout", typst.Options{Timeout: -time.Second}, "must not be negative"},
		{"negative output size", typst.Options{MaximumOutputSize: -1}, "must not be negative"},
		{"nil observer", typst.Options{Observers: []typst.Observer{nil}}, "nil Observer"},
		{"missing font path", typst.Options{FontPaths: []string{filepath.Join(t.TempDir(), "fonts")}}, "font path"},
		{"font path is a file", typst.Options{FontPaths: []string{file}}, "is not a directory"},
		{"two font paths in one", typst.Options{FontPaths: []string{t.TempDir() + string(filepath.ListSeparator) + t.TempDir()}}, "must be one directory"},
		{"empty font path", typst.Options{FontPaths: []string{""}}, "font path must not be empty"},
		{"missing package path", typst.Options{PackagePath: filepath.Join(t.TempDir(), "packages")}, "package path"},
		{"missing temporary directory", typst.Options{TemporaryDirectory: filepath.Join(t.TempDir(), "missing")}, "temporary directory"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.options.Binary == "" {
				testCase.options.Binary = fakeBinary(t, "0.15.1")
			}
			_, err := typst.New(&testCase.options)
			require.ErrorContains(t, err, testCase.want)
		})
	}
}

func TestNewAcceptsAMatchingBinary(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{Binary: fakeBinary(t, "0.16.2"), Version: "0.16.2"})
	require.Equal(t, "0.16.2", compiler.Version())

	compiler = newCompiler(t, &typst.Options{Binary: fakeBinary(t, "0.13.0")})
	require.Equal(t, "0.13.0", compiler.Version())
}

// Only font paths are joined into one argument; the package path and the temporary directory
// may contain the list separator.
func TestSingleDirectoriesMayContainTheListSeparator(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "a"+string(filepath.ListSeparator)+"b")
	require.NoError(t, os.Mkdir(directory, 0o700))
	compiler := newCompiler(t, &typst.Options{PackagePath: directory, TemporaryDirectory: directory})
	report := echo(t, compiler, &typst.Document{Source: []byte("echo")})
	require.Contains(t, report.Arguments, directory)
}

func TestNilOptionsLookUpTypstOnThePath(t *testing.T) {
	directory := t.TempDir()
	executable, err := os.Executable()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(executable, filepath.Join(directory, typst.DefaultBinary)))
	t.Setenv("PATH", directory)

	compiler, err := typst.New(nil)
	require.NoError(t, err)
	require.Equal(t, "0.15.1", compiler.Version())
}

func TestCompilePassesOnlyOptionsOnTheCommandLine(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	report := echo(t, compiler, &typst.Document{Source: []byte("echo"), Data: map[string]string{"name": "--root /etc #evil"}})

	require.Len(t, report.Arguments, 14)
	require.Equal(t, []string{
		"compile", "--root", ".", "--diagnostic-format", "short", "--format", "pdf",
		"--package-path", report.Arguments[8], "--package-cache-path", report.Arguments[10], "--ignore-system-fonts",
		"main.typ", "-",
	}, report.Arguments)
	// Both package directories are fresh, outside the project root and empty: nothing to import, nothing to download into.
	require.Equal(t, "packages", filepath.Base(report.Arguments[8]))
	require.Equal(t, "cache", filepath.Base(report.Arguments[10]))
	require.Equal(t, filepath.Dir(report.Arguments[8]), filepath.Dir(report.Arguments[10]))

	require.ElementsMatch(t, []string{
		"HTTP_PROXY=http://127.0.0.1:0", "HTTPS_PROXY=http://127.0.0.1:0", "ALL_PROXY=http://127.0.0.1:0",
		"http_proxy=http://127.0.0.1:0", "https_proxy=http://127.0.0.1:0", "all_proxy=http://127.0.0.1:0",
	}, report.Environment)
}

func TestCompileWritesTheProject(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	report := echo(t, compiler, &typst.Document{
		Source: []byte("echo"),
		Data:   map[string]any{"title": "Q3 report #1 *draft*", "count": 3},
		Files: map[string][]byte{
			"logo.svg":               []byte("<svg/>"),
			"chapters/one/intro.typ": []byte("= Intro"),
		},
	})
	require.Equal(t, map[string]string{
		"main.typ":               "echo",
		"data.json":              `{"count":3,"title":"Q3 report #1 *draft*"}`,
		"logo.svg":               "<svg/>",
		"chapters/one/intro.typ": "= Intro",
	}, report.Files)

	report = echo(t, compiler, &typst.Document{Source: []byte("echo")})
	require.Equal(t, map[string]string{"main.typ": "echo"}, report.Files, "no data means no data file")
}

func TestCompileFormatOptions(t *testing.T) {
	fonts, otherFonts, packages := t.TempDir(), t.TempDir(), t.TempDir()
	compiler := newCompiler(t, &typst.Options{FontPaths: []string{fonts, otherFonts}, SystemFonts: true, PackagePath: packages})

	report := echo(t, compiler, &typst.Document{Source: []byte("echo"), Format: typst.FormatPNG, Page: 3, PixelsPerInch: 300})
	require.Equal(t, []string{"--format", "png"}, report.Arguments[5:7])
	require.Equal(t, []string{"--package-path", packages}, report.Arguments[7:9])
	require.NotContains(t, report.Arguments, "--ignore-system-fonts")
	require.Equal(t, []string{
		"--font-path", fonts + string(filepath.ListSeparator) + otherFonts,
		"--pages", "3", "--ppi", "300", "main.typ", "-",
	}, report.Arguments[11:])

	report = echo(t, compiler, &typst.Document{Source: []byte("echo"), Format: typst.FormatSVG})
	require.Equal(t, []string{"--pages", "1", "main.typ", "-"}, report.Arguments[len(report.Arguments)-4:], "an image renders the first page by default")
}

func TestCompileRefusesAnInvalidDocument(t *testing.T) {
	observer := &recordingObserver{}
	compiler := newCompiler(t, &typst.Options{Observers: []typst.Observer{observer}})
	source := []byte("echo")
	cases := map[string]*typst.Document{
		"nil":                   nil,
		"no source":             {},
		"unknown format":        {Source: source, Format: "html"},
		"page of a PDF":         {Source: source, Page: 2},
		"negative page":         {Source: source, Format: typst.FormatPNG, Page: -1},
		"resolution of an SVG":  {Source: source, Format: typst.FormatSVG, PixelsPerInch: 300},
		"negative resolution":   {Source: source, Format: typst.FormatPNG, PixelsPerInch: -1},
		"data that is no JSON":  {Source: source, Data: make(chan int)},
		"parent directory":      {Source: source, Files: map[string][]byte{"../escape.typ": nil}},
		"nested parent":         {Source: source, Files: map[string][]byte{"images/../../escape.typ": nil}},
		"absolute path":         {Source: source, Files: map[string][]byte{"/etc/passwd": nil}},
		"backslash":             {Source: source, Files: map[string][]byte{`images\logo.png`: nil}},
		"empty element":         {Source: source, Files: map[string][]byte{"images//logo.png": nil}},
		"trailing slash":        {Source: source, Files: map[string][]byte{"images/": nil}},
		"dot":                   {Source: source, Files: map[string][]byte{".": nil}},
		"empty path":            {Source: source, Files: map[string][]byte{"": nil}},
		"the source file":       {Source: source, Files: map[string][]byte{"main.typ": nil}},
		"the data file in caps": {Source: source, Files: map[string][]byte{"DATA.json": nil}},
		"below the source file": {Source: source, Files: map[string][]byte{"main.typ/x": nil}},
		"below the data file":   {Source: source, Files: map[string][]byte{"Data.json/x/y": nil}},
		"a file and a dir":      {Source: source, Files: map[string][]byte{"a": nil, "a/b": nil}},
		"a file and a dir case": {Source: source, Files: map[string][]byte{"A": nil, "a/b": nil}},
		"names differing case":  {Source: source, Files: map[string][]byte{"Logo.svg": nil, "logo.svg": nil}},
		"nul byte":              {Source: source, Files: map[string][]byte{"a\x00b": nil}},
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := compiler.Compile(t.Context(), document)
			require.ErrorIs(t, err, typst.ErrInvalidDocument)
		})
	}
	require.Empty(t, observer.events(), "an invalid document starts no compile")
}

func TestCompileReportsTheDiagnostics(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeFail)})

	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Equal(t, 1, compileError.ExitCode)
	require.Equal(t, "main.typ:1:1: warning: something minor\nmain.typ:2:5: error: unknown variable: nope\n", compileError.Diagnostics)
	require.EqualError(t, err, "typst: compile failed: main.typ:2:5: error: unknown variable: nope")
}

func TestCompileCapsTheDiagnostics(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeFlood)})

	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Len(t, compileError.Diagnostics, 64<<10)
}

func TestCompileErrorWithoutDiagnostics(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeExit)})

	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Equal(t, 3, compileError.ExitCode)
	require.EqualError(t, err, "typst: compile failed: no diagnostics")
	require.EqualError(t, &typst.CompileError{Diagnostics: "panicked\nat line 2"}, "typst: compile failed: panicked")
}

func TestCompileKillsAProcessThatRunsTooLong(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{Timeout: time.Millisecond})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeHang)})
	require.ErrorIs(t, err, typst.ErrTimeout)
}

func TestCompileStopsWhenTheCallerCancels(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := compiler.Compile(ctx, &typst.Document{Source: []byte(fakeHang)})
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, typst.ErrTimeout)
}

func TestCompileReturnsTheContextsErrorNotItsCause(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	// errgroup.WithContext cancels the others with the first failure as the cause.
	cause := &typst.CompileError{ExitCode: 1, Diagnostics: "main.typ:1:1: error: in another document"}
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	_, err := compiler.Compile(ctx, &typst.Document{Source: []byte(fakeHang)})
	require.ErrorIs(t, err, context.Canceled)
	var compileError *typst.CompileError
	require.False(t, errors.As(err, &compileError), "the cause is not in the chain: %v", err)
}

func TestCompileCapsTheOutput(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{MaximumOutputSize: 100_000})

	output, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeLargePrefix + "100000")})
	require.NoError(t, err)
	require.Len(t, output, 100_000)

	_, err = compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeLargePrefix + "100001")})
	require.ErrorIs(t, err, typst.ErrOutputTooLarge)
}

func TestCompileWithoutOutput(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})

	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeEmpty), Format: typst.FormatPNG, Page: 9})
	require.ErrorIs(t, err, typst.ErrPageNotFound)

	_, err = compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeEmpty)})
	require.EqualError(t, err, "typst: the compile produced no output")
}

func TestCompileRemovesTheRenderDirectory(t *testing.T) {
	temporary := t.TempDir()
	compiler := newCompiler(t, &typst.Options{TemporaryDirectory: temporary})
	echo(t, compiler, &typst.Document{Source: []byte("echo"), Data: "secret"})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeFail)})
	require.Error(t, err)

	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestCompileWithoutATemporaryDirectory(t *testing.T) {
	temporary := filepath.Join(t.TempDir(), "gone")
	require.NoError(t, os.Mkdir(temporary, 0o700))
	compiler := newCompiler(t, &typst.Options{TemporaryDirectory: temporary})
	require.NoError(t, os.Remove(temporary))
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte("echo")})
	require.ErrorContains(t, err, "create the render directory")
}

func TestCompileReportsARenderDirectoryLeftBehind(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root removes a directory without write permission, so the fake cannot lock it")
	}
	temporary := t.TempDir()
	t.Cleanup(func() { unlockRenderDirectories(t, temporary) })
	compiler := newCompiler(t, &typst.Options{TemporaryDirectory: temporary})

	for _, source := range []string{fakeLock, fakeLock + " fail"} {
		_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(source)})
		require.ErrorContains(t, err, "remove the render directory")
	}
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeLock + " fail")})
	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError, "the compile's own error stays matchable")
}

// unlockRenderDirectories gives back the write permission fakeLock took, so the test's
// temporary directory can be removed.
func unlockRenderDirectories(t *testing.T, temporary string) {
	t.Helper()
	entries, err := os.ReadDir(temporary)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NoError(t, os.Chmod(filepath.Join(temporary, entry.Name()), 0o700)) //nolint:gosec // G302: a directory needs the execute bit
	}
}

func TestCompileReportsTheWarningsOfASuccess(t *testing.T) {
	observer := &recordingObserver{}
	compiler := newCompiler(t, &typst.Options{Observers: []typst.Observer{observer}})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeWarn)})
	require.NoError(t, err)
	require.Equal(t, "main.typ:1:16: warning: unknown font family: inter\n", observer.outcomes[0].Warnings)

	_, err = compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeFail)})
	require.Error(t, err)
	require.Empty(t, observer.outcomes[1].Warnings, "a failure's warnings are in its diagnostics")
}

func TestCompileIsSafeForConcurrentUse(t *testing.T) {
	compiler := newCompiler(t, &typst.Options{})
	var group sync.WaitGroup
	reports := make([]fakeReport, 8)
	for index := range reports {
		group.Go(func() {
			output, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte("echo"), Data: index})
			if err == nil {
				err = json.Unmarshal(output, &reports[index])
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	for index, report := range reports {
		require.Equal(t, strconv.Itoa(index), report.Files["data.json"])
	}
}

// recordingObserver records the calls it receives.
type recordingObserver struct {
	mutex    sync.Mutex
	name     string
	log      *[]string
	outcomes []*typst.Outcome
	compiles []*typst.Compile
}

type observerKey struct{}

func (o *recordingObserver) events() []string {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	if o.log == nil {
		return nil
	}
	return *o.log
}

func (o *recordingObserver) record(event string) {
	if o.log == nil {
		o.log = &[]string{}
	}
	*o.log = append(*o.log, event)
}

func (o *recordingObserver) CompileStarted(ctx context.Context, compile *typst.Compile) context.Context {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	o.record(o.name + " started")
	o.compiles = append(o.compiles, compile)
	return context.WithValue(ctx, observerKey{}, o.name)
}

func (o *recordingObserver) CompileFinished(ctx context.Context, _ *typst.Compile, outcome *typst.Outcome) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	value, _ := ctx.Value(observerKey{}).(string)
	o.record(o.name + " finished in context of " + value)
	o.outcomes = append(o.outcomes, outcome)
}

func TestObserversSeeEveryCompile(t *testing.T) {
	log := &[]string{}
	first := &recordingObserver{name: "first", log: log}
	second := &recordingObserver{name: "second", log: log}
	compiler := newCompiler(t, &typst.Options{Observers: []typst.Observer{first, second}, MaximumOutputSize: 10})

	_, err := compiler.Compile(t.Context(), &typst.Document{Name: "report", Source: []byte(fakeLargePrefix + "10"), Format: typst.FormatSVG})
	require.NoError(t, err)
	require.Equal(t, []string{"first started", "second started", "second finished in context of second", "first finished in context of second"}, *log)
	require.Equal(t, &typst.Compile{Name: "report", Format: typst.FormatSVG}, first.compiles[0])
	outcome := first.outcomes[0]
	require.Equal(t, typst.ResultSuccess, outcome.Result)
	require.Equal(t, 10, outcome.OutputSize)
	require.NoError(t, outcome.Err)
	require.Positive(t, outcome.Duration)

	failures := map[string]struct {
		cancelled bool
		source    string
		format    typst.Format
		want      typst.Result
	}{
		"compile error":    {false, fakeFail, "", typst.ResultCompileError},
		"page not found":   {false, fakeEmpty, typst.FormatPNG, typst.ResultCompileError},
		"output too large": {false, fakeLargePrefix + "11", "", typst.ResultOutputTooLarge},
		"no output":        {false, fakeEmpty, "", typst.ResultFailure},
		"cancelled":        {true, fakeHang, "", typst.ResultCancelled},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if failure.cancelled {
				cancel()
			}
			defer cancel()
			_, err := compiler.Compile(ctx, &typst.Document{Source: []byte(failure.source), Format: failure.format})
			outcome := second.outcomes[len(second.outcomes)-1]
			require.Equal(t, failure.want, outcome.Result)
			require.Equal(t, err, outcome.Err)
			require.Zero(t, outcome.OutputSize)
		})
	}

	timeouts := &recordingObserver{}
	compiler = newCompiler(t, &typst.Options{Observers: []typst.Observer{timeouts}, Timeout: time.Millisecond})
	_, err = compiler.Compile(t.Context(), &typst.Document{Source: []byte(fakeHang)})
	require.True(t, errors.Is(err, typst.ErrTimeout))
	require.Equal(t, typst.ResultTimeout, timeouts.outcomes[0].Result)
}
