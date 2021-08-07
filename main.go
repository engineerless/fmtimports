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
	"runtime"
	"sort"
	"strings"
)

var (
	list   = flag.Bool("l", false, "list files whose formatting differs from gofmt's")
	doDiff = flag.Bool("d", false, "display diffs instead of rewriting files")
	write  = flag.Bool("w", false, "write result to (source) file instead of stdout")
	// TODO: remove ignore-file flag when code-gen starts to generate ordered imports
	ignoreFile = flag.String("ignore-file", "zz_generated", "files matching this regex are ignored")
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
	exitCode   = 0
	parserMode = parser.ParseComments
)

func report(err error) {
	scanner.PrintError(os.Stderr, err)
	exitCode = 2
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: gofmt-import [flags] [path ...]\n")
	flag.PrintDefaults()
}

func filterFile(f fs.DirEntry) bool {
	// ignore non-Go files and files matching ignoreFile
	name := f.Name()
	if f.IsDir() ||
		strings.HasPrefix(name, ".") ||
		!strings.HasSuffix(name, ".go") {
		return true
	}
	if *ignoreFile != "" && strings.Contains(name, *ignoreFile) {
		return true
	}
	return false
}

// If in == nil, the source is the contents of the file with the given filename.
func processFile(filename string, in io.Reader, out io.Writer, stdin bool) error {
	fileSet := token.NewFileSet() // per file FileSet

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

		d.Specs = sortDecl(fset, d)

	}
}

func sortDecl(fSet *token.FileSet, d *ast.GenDecl) []ast.Spec {

	specs := d.Specs
	start := d.Pos()
	end := d.End()

	var stdlibImports, localImports, k8sImports, externalImports []*ast.ImportSpec

	// split all imports into different groups
	for _, spec := range specs {
		imp := spec.(*ast.ImportSpec)
		importPath := strings.Replace(imp.Path.Value, "\"", "", -1)
		parts := strings.Split(importPath, "/")
		if !strings.Contains(parts[0], ".") {
			// standard library
			stdlibImports = append(stdlibImports, imp)
		} else if strings.HasPrefix(importPath, "k8s.io/kubernetes") {
			// local imports
			localImports = append(localImports, imp)
		} else if strings.Contains(parts[0], "k8s.io") {
			// other *.k8s.io imports
			k8sImports = append(k8sImports, imp)
		} else {
			// external repositories
			externalImports = append(externalImports, imp)
		}
	}

	// reset each import's start and end position
	orderedImports := make([]ast.Spec, 0)
	impLines := make([]int, 0)

	// Pos() of the line next `import (`
	offset := fSet.File(start).Offset(fSet.File(start).LineStart(fSet.File(start).Line(start) + 1))

	for _, gImps := range [][]*ast.ImportSpec{
		stdlibImports,
		externalImports,
		k8sImports,
		localImports,
	} {
		sort.SliceStable(gImps, func(i, j int) bool {
			return gImps[i].Path.Value < gImps[j].Path.Value
		})

		for _, imp := range gImps {
			impLines = append(impLines, offset)
			// calculate and set new startPos,endPos for import spec
			sPos := token.Pos(offset + 1)
			ePos := token.Pos(int(sPos) + (int(imp.End()) - int(imp.Pos())))
			if imp.Name != nil {
				imp.Name.NamePos = sPos
			}
			imp.Path.ValuePos = sPos
			imp.EndPos = ePos
			orderedImports = append(orderedImports, imp)

			offset = int(ePos)
		}
		// add black lines
		if len(gImps) != 0 {
			impLines = append(impLines, offset)
			offset++
		}
	}
	// update line offset table,since we may have added new blank lines
	// the line is `import (`
	impStartLine := fSet.Position(start).Line
	// the line after `)`
	impEndLine := fSet.Position(end).Line

	lines := make([]int, 0)
	impFile := fSet.File(start)
	for i := 1; i <= impStartLine; i++ {
		lines = append(lines, impFile.Offset(impFile.LineStart(i)))
	}

	lines = append(lines, impLines...)

	for i := impEndLine + 1; i <= impFile.LineCount(); i++ {
		lines = append(lines, impFile.Offset(impFile.LineStart(i)))
	}

	impFile.SetLines(lines)
	return orderedImports

}

func visitFile(path string, f fs.DirEntry, err error) error {
	if err == nil && !filterFile(f) {
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
	// call gofmtMain in a separate function
	// so that it can use defer and have them
	// run before the exit.
	gofmtMain()
	os.Exit(exitCode)
}

func gofmtMain() {
	flag.Usage = usage
	flag.Parse()

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
