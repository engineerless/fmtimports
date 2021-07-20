package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	list      = flag.Bool("l", false, "list files whose formatting differs from gofmt's")
	doDiff    = flag.Bool("d", false, "display diffs instead of rewriting files")
	orderRule = flag.String("r", "", "order rule (e.g., '^\"github.*\"$ ^\"k8s.*\"$' )")
	write     = flag.Bool("w", false, "write result to (source) file instead of stdout")
	allErrors = flag.Bool("e", false, "report all errors (not just the first 10 on different lines)")
)

const (
	tabWidth    = 8
	printerMode = printer.UseSpaces | printer.TabIndent | printerNormalizeNumbers

	// printerNormalizeNumbers means to canonicalize number literal prefixes
	// and exponents while printing. See https://golang.org/doc/go1.13#gofmt.
	//
	// This value is defined in go/printer specifically for go/format and cmd/gofmt.
	printerNormalizeNumbers = 1 << 30
)

var (
	fileSet      = token.NewFileSet() // per process FileSet
	exitCode     = 0
	rulePatterns = make([]*regexp.Regexp, 0)
	parserMode   parser.Mode
)

func report(err error) {
	scanner.PrintError(os.Stderr, err)
	exitCode = 2
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: gofmt-import [flags] [path ...]\n")
	flag.PrintDefaults()
}

func initParserMode() {
	parserMode = parser.ParseComments
	if *allErrors {
		parserMode |= parser.AllErrors
	}
}

func initRules() {
	rulesStr := make([]string, 0)
	rulesStr = append(rulesStr, `^"\w*"$`) // standard lib
	if *orderRule != "" {
		for _, r := range strings.Split(*orderRule, " ") {
			rulesStr = append(rulesStr, r)
		}
	}
	rulesStr = append(rulesStr, `^".*"$`)
	for _, r := range rulesStr {
		p, _ := regexp.Compile(r)
		rulePatterns = append(rulePatterns, p)
	}

}

func isGoFile(f fs.DirEntry) bool {
	// ignore non-Go files
	name := f.Name()
	return !f.IsDir() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".go")
}

// If in == nil, the source is the contents of the file with the given filename.
func processFile(filename string, in io.Reader, out io.Writer, stdin bool) error {
	var perm fs.FileMode = 0644
	if in == nil {
		f, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			return err
		}
		in = f
		perm = fi.Mode().Perm()
	}

	src, err := io.ReadAll(in)
	if err != nil {
		return err
	}

	astFile, err := parser.ParseFile(fileSet, filename, src, parserMode)
	if err != nil {
		return err
	}

	sortImports(fileSet, astFile)
	ast.SortImports(fileSet, astFile)

	var buf bytes.Buffer
	cfg := printer.Config{Mode: printerMode, Tabwidth: tabWidth}
	err = cfg.Fprint(&buf, fileSet, astFile)
	if err != nil {
		return err
	}
	res := buf.Bytes()

	if !bytes.Equal(src, res) {
		// formatting has changed
		if *list {
			fmt.Fprintln(out, filename)
		}
		if *write {
			// make a temporary backup before overwriting original
			bakname, err := backupFile(filename+".", src, perm)
			if err != nil {
				return err
			}
			err = os.WriteFile(filename, res, perm)
			if err != nil {
				os.Rename(bakname, filename)
				return err
			}
			err = os.Remove(bakname)
			if err != nil {
				return err
			}
		}
		if *doDiff {
			data, err := diffWithReplaceTempFile(src, res, filename)
			if err != nil {
				return fmt.Errorf("computing diff: %s", err)
			}
			fmt.Printf("diff -u %s %s\n", filepath.ToSlash(filename+".orig"), filepath.ToSlash(filename))
			out.Write(data)
		}
	}
	if !*list && !*write && !*doDiff {
		_, err = out.Write(res)
	}

	return err
}

func sortImports(fset *token.FileSet, f *ast.File) {
	for _, d := range f.Decls {
		d, ok := d.(*ast.GenDecl)
		if !ok || d.Tok != token.IMPORT {
			// Not an import declaration, so we're done.
			// Imports are always first.
			break
		}

		if !d.Lparen.IsValid() {
			// Not a block: sorted by default.
			continue
		}

		if len(d.Specs) <= 1 {
			continue
		}

		d.Specs = sortImportSpecs(fset, f, d.Specs, posSpan{Start: d.Pos(), End: d.End()})
	}
}

func sortImportSpecs(fSet *token.FileSet, astFile *ast.File, specs []ast.Spec, importPos posSpan) []ast.Spec {

	startPos := importPos.Start
	line := fSet.Position(startPos).Line + 1
	startFile := fSet.File(startPos)

	startOffset := startFile.Offset(startPos)
	importLines := make([]int, 0)
	startOffset++

	poses := make([]posSpan, 0)
	for _, spec := range specs {
		poses = append(poses, posSpan{
			Start: spec.Pos(),
			End:   spec.End(),
		})
	}

	specsRes := make([]ast.Spec, 0)
	specMatched := make([]bool, len(specs))

	for _, pattern := range rulePatterns {
		for index, spec := range specs {
			if specMatched[index] {
				continue
			}
			iSpec := spec.(*ast.ImportSpec)
			if matched := pattern.MatchString(iSpec.Path.Value); matched {
				specMatched[index] = true
				importLines = append(importLines, startOffset)

				sPos := token.Pos(startOffset + 1)

				ePos := token.Pos(int(sPos) + (int(poses[index].End) - int(poses[index].Start)))
				if iSpec.Name != nil {
					iSpec.Name.NamePos = sPos
				}
				iSpec.Path.ValuePos = sPos
				iSpec.EndPos = ePos
				specsRes = append(specsRes, iSpec)
				startOffset = int(ePos)
				line++
			}

		}
		importLines = append(importLines, startOffset)
		line++
		startOffset++
	}

	startLineIndex := fSet.Position(startPos).Line
	endLineIndex := fSet.Position(importPos.End).Line

	// LineStart
	lines := make([]int, 0)
	for i := 1; i <= startLineIndex; i++ {
		lines = append(lines, startFile.Offset(startFile.LineStart(i)))
	}
	lines = append(lines, importLines...)

	for i := endLineIndex; i <= startFile.LineCount(); i++ {
		lines = append(lines, startFile.Offset(startFile.LineStart(i)))
	}

	fSet.File(startPos).SetLines(lines)
	return specsRes
}

type posSpan struct {
	Start token.Pos
	End   token.Pos
}

func visitFile(path string, f fs.DirEntry, err error) error {
	if err == nil && isGoFile(f) {
		err = processFile(path, nil, os.Stdout, false)
	}
	// Don't complain if a file was deleted in the meantime (i.e.
	// the directory changed concurrently while running gofmt).
	if err != nil && !os.IsNotExist(err) {
		report(err)
	}
	return nil
}

func walkDir(path string) {
	filepath.WalkDir(path, visitFile)
}

func main() {
	gofmtMain()
	os.Exit(exitCode)
}

func gofmtMain() {
	flag.Usage = usage
	flag.Parse()

	initParserMode()
	initRules()
	if flag.NArg() == 0 {
		if *write {
			fmt.Fprintln(os.Stderr, "error: cannot use -w with standard input")
			exitCode = 2
			return
		}
		if err := processFile("<standard input>", os.Stdin, os.Stdout, true); err != nil {
			report(err)
		}
		return
	}

	for i := 0; i < flag.NArg(); i++ {
		path := flag.Arg(i)
		switch dir, err := os.Stat(path); {
		case err != nil:
			report(err)
		case dir.IsDir():
			walkDir(path)
		default:
			if err := processFile(path, nil, os.Stdout, false); err != nil {
				report(err)
			}
		}
	}
}

func diffWithReplaceTempFile(b1, b2 []byte, filename string) ([]byte, error) {
	data, err := diff("gofmt", b1, b2)
	if len(data) > 0 {
		return replaceTempFilename(data, filename)
	}
	return data, err
}

// Returns diff of two arrays of bytes in diff tool format.
func diff(prefix string, b1, b2 []byte) ([]byte, error) {
	f1, err := writeTempFile(prefix, b1)
	if err != nil {
		return nil, err
	}
	defer os.Remove(f1)

	f2, err := writeTempFile(prefix, b2)
	if err != nil {
		return nil, err
	}
	defer os.Remove(f2)

	cmd := "diff"
	if runtime.GOOS == "plan9" {
		cmd = "/bin/ape/diff"
	}

	data, err := exec.Command(cmd, "-u", f1, f2).CombinedOutput()
	if len(data) > 0 {
		// diff exits with a non-zero status when the files don't match.
		// Ignore that failure as long as we get output.
		err = nil
	}
	return data, err
}

func writeTempFile(prefix string, data []byte) (string, error) {
	file, err := ioutil.TempFile("", prefix)
	if err != nil {
		return "", err
	}
	_, err = file.Write(data)
	if err1 := file.Close(); err == nil {
		err = err1
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

// replaceTempFilename replaces temporary filenames in diff with actual one.
//
// --- /tmp/gofmt316145376	2017-02-03 19:13:00.280468375 -0500
// +++ /tmp/gofmt617882815	2017-02-03 19:13:00.280468375 -0500
// ...
// ->
// --- path/to/file.go.orig	2017-02-03 19:13:00.280468375 -0500
// +++ path/to/file.go	2017-02-03 19:13:00.280468375 -0500
// ...
func replaceTempFilename(diff []byte, filename string) ([]byte, error) {
	bs := bytes.SplitN(diff, []byte{'\n'}, 3)
	if len(bs) < 3 {
		return nil, fmt.Errorf("got unexpected diff for %s", filename)
	}
	// Preserve timestamps.
	var t0, t1 []byte
	if i := bytes.LastIndexByte(bs[0], '\t'); i != -1 {
		t0 = bs[0][i:]
	}
	if i := bytes.LastIndexByte(bs[1], '\t'); i != -1 {
		t1 = bs[1][i:]
	}
	// Always print filepath with slash separator.
	f := filepath.ToSlash(filename)
	bs[0] = []byte(fmt.Sprintf("--- %s%s", f+".orig", t0))
	bs[1] = []byte(fmt.Sprintf("+++ %s%s", f, t1))
	return bytes.Join(bs, []byte{'\n'}), nil
}

const chmodSupported = runtime.GOOS != "windows"

// backupFile writes data to a new file named filename<number> with permissions perm,
// with <number randomly chosen such that the file name is unique. backupFile returns
// the chosen file name.
func backupFile(filename string, data []byte, perm fs.FileMode) (string, error) {
	// create backup file
	f, err := os.CreateTemp(filepath.Dir(filename), filepath.Base(filename))
	if err != nil {
		return "", err
	}
	bakname := f.Name()
	if chmodSupported {
		err = f.Chmod(perm)
		if err != nil {
			f.Close()
			os.Remove(bakname)
			return bakname, err
		}
	}

	// write data to backup file
	_, err = f.Write(data)
	if err1 := f.Close(); err == nil {
		err = err1
	}

	return bakname, err
}
