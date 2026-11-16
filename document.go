package typst

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// The files every render directory holds besides Document.Files. A template reads the data with
// json("/data.json").
const (
	SourceFile = "main.typ"
	DataFile   = "data.json"
)

// Format is the output format of a compile.
type Format string

// The formats Compile produces.
const (
	FormatPDF Format = "pdf"
	FormatSVG Format = "svg"
	FormatPNG Format = "png"
)

// Document is one render: a template, the files it reads and its data.
type Document struct {
	// Name names the template ("invoice", "task-report") in metrics and spans. Use a constant
	// of the application, never user input: it becomes a label. Empty is allowed.
	Name string
	// Source is the Typst template, written as SourceFile; required.
	Source []byte
	// Data is encoded with encoding/json and written as DataFile; nil writes no data file.
	// Strings in it reach the template as data, never as markup.
	Data any
	// Files are further files the template reads (images, includes, bibliographies), by a
	// slash-separated path relative to the project root, such as "images/logo.png". A path must
	// be valid for io/fs, may not contain a backslash or NUL and may not be SourceFile, DataFile,
	// a directory of another path or, ignoring case, equal to another path.
	// Fonts are not found here; put them into a directory of Options.FontPaths.
	Files map[string][]byte
	// Format is the output format; empty means FormatPDF.
	Format Format
	// Page selects the one page an SVG or PNG compile renders, counted from 1; zero means the
	// first. A PDF always holds every page, so Page must be zero for FormatPDF.
	Page int
	// PixelsPerInch is the resolution of a PNG; zero means typst's default of 144. Only valid
	// for FormatPNG.
	PixelsPerInch int
}

func (d *Document) format() Format {
	if d.Format == "" {
		return FormatPDF
	}
	return d.Format
}

// validate checks everything Compile can check before writing a file.
func (d *Document) validate() error {
	if d == nil || len(d.Source) == 0 {
		return fmt.Errorf("%w: no source", ErrInvalidDocument)
	}
	format := d.format()
	switch format {
	case FormatPDF, FormatSVG, FormatPNG:
	default:
		return fmt.Errorf("%w: unknown format %q", ErrInvalidDocument, d.Format)
	}
	if d.Page < 0 || (d.Page > 0 && format == FormatPDF) {
		return fmt.Errorf("%w: page %d is not valid for %s", ErrInvalidDocument, d.Page, format)
	}
	if d.PixelsPerInch < 0 || (d.PixelsPerInch > 0 && format != FormatPNG) {
		return fmt.Errorf("%w: pixels per inch %d is not valid for %s", ErrInvalidDocument, d.PixelsPerInch, format)
	}
	return validateFileNames(d.Files)
}

// validateFileNames checks that every name of files is a path inside the project and that no
// two paths collide: none equals a reserved file or another path, and none is a directory of
// another. Names are compared case-insensitively, because the render directory may live on a
// case-insensitive file system.
func validateFileNames(files map[string][]byte) error {
	taken := map[string]bool{strings.ToLower(SourceFile): true, strings.ToLower(DataFile): true}
	directories := map[string]string{}
	for name := range files {
		if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\\x00") {
			return fmt.Errorf("%w: file path %q is not a relative slash-separated path inside the project", ErrInvalidDocument, name)
		}
		folded := strings.ToLower(name)
		if taken[folded] {
			return fmt.Errorf("%w: file path %q is reserved or given twice", ErrInvalidDocument, name)
		}
		taken[folded] = true
		for directory := path.Dir(folded); directory != "."; directory = path.Dir(directory) {
			directories[directory] = name
		}
	}
	for directory, name := range directories {
		if taken[directory] {
			return fmt.Errorf("%w: file path %q is below a file", ErrInvalidDocument, name)
		}
	}
	return nil
}

// encodeData returns the JSON of Data, nil when there is no data.
func (d *Document) encodeData() ([]byte, error) {
	if d.Data == nil {
		return nil, nil
	}
	data, err := json.Marshal(d.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: encode the data: %v", ErrInvalidDocument, err) //nolint:errorlint // LIB-010: encoding/json errors are not part of the API
	}
	return data, nil
}
