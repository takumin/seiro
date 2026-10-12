// Package seirofile locates and evaluates Seirofiles.
package seirofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/takumin/seiro/internal/spec"
)

// Name is the file name of a Seirofile.
const Name = "Seirofile"

// ErrNotFound is returned by Find when no Seirofile exists in the directory or its parents.
var ErrNotFound = errors.New("no Seirofile found")

// Find searches dir and its parents for a Seirofile and returns its absolute path.
func Find(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(d, Name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("%w in %s or any parent directory", ErrNotFound, dir)
		}
		d = parent
	}
}

// Resolve returns path when it is set, and otherwise the Seirofile found by
// searching the working directory and its parents.
func Resolve(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return Find(wd)
}

var fileOptions = &syntax.FileOptions{
	Set:             true,
	While:           true,
	TopLevelControl: true,
	GlobalReassign:  true,
}

// Load reads and evaluates the Seirofile at path, then validates the result.
func Load(path string) (*spec.Environment, error) {
	src, err := os.ReadFile(path) // #nosec G304 -- the Seirofile path is chosen by the user
	if err != nil {
		return nil, err
	}
	env, err := Eval(path, src)
	if err != nil {
		return nil, err
	}
	if err := env.Validate(); err != nil {
		return nil, err
	}
	return env, nil
}

// Eval evaluates Seirofile source. path is used for error messages and to resolve
// relative paths given to file().
func Eval(path string, src []byte) (*spec.Environment, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	b := &builder{dir: filepath.Dir(abs), env: &spec.Environment{}}
	thread := &starlark.Thread{
		Name: abs,
		Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
			return nil, errors.New("load() is not supported yet")
		},
	}
	if _, err := starlark.ExecFileOptions(fileOptions, thread, abs, src, b.predeclared()); err != nil {
		var evalErr *starlark.EvalError
		if errors.As(err, &evalErr) {
			return nil, errors.New(evalErr.Backtrace())
		}
		return nil, err
	}
	return b.env, nil
}
