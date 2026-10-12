package main

import (
	"io"
	"os"
	"testing"

	"github.com/takumin/seiro/internal/command"
)

func TestMainCommand(t *testing.T) {
	var capture int
	osExit = func(code int) { capture = code }

	ro, wo, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	re, we, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	stdout := os.Stdout
	stderr := os.Stderr
	defer func() {
		os.Stdout = stdout
		os.Stderr = stderr
	}()
	os.Stdout = wo
	os.Stderr = we

	os.Args = []string{"a"}

	main()

	if err := wo.Close(); err != nil {
		t.Fatalf("failed stdout close error: %v", err)
	}
	if err := we.Close(); err != nil {
		t.Fatalf("failed stderr close error: %v", err)
	}

	io.Copy(io.Discard, ro) //nolint:all
	io.Copy(io.Discard, re) //nolint:all

	expect := command.ExitOK
	actual := capture

	if expect != actual {
		t.Errorf(
			"Fail assert equal. Expect: %v Actual: %v",
			expect,
			actual,
		)
	}
}
