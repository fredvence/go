// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package carchive_test

import (
	"bufio"
	"debug/elf"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode"
)

// Program to run.
var bin []string

// C compiler with args (from $(go env CC) $(go env GOGCCFLAGS)).
var cc []string

// An environment with GOPATH=$(pwd).
var gopathEnv []string

// ".exe" on Windows.
var exeSuffix string

var GOOS, GOARCH string
var libgodir string

func init() {
	GOOS = goEnv("GOOS")
	GOARCH = goEnv("GOARCH")
	bin = cmdToRun("./testp")

	ccOut := goEnv("CC")
	cc = []string{string(ccOut)}

	out := goEnv("GOGCCFLAGS")
	quote := '\000'
	start := 0
	lastSpace := true
	backslash := false
	s := string(out)
	for i, c := range s {
		if quote == '\000' && unicode.IsSpace(c) {
			if !lastSpace {
				cc = append(cc, s[start:i])
				lastSpace = true
			}
		} else {
			if lastSpace {
				start = i
				lastSpace = false
			}
			if quote == '\000' && !backslash && (c == '"' || c == '\'') {
				quote = c
				backslash = false
			} else if !backslash && quote == c {
				quote = '\000'
			} else if (quote == '\000' || quote == '"') && !backslash && c == '\\' {
				backslash = true
			} else {
				backslash = false
			}
		}
	}
	if !lastSpace {
		cc = append(cc, s[start:])
	}

	if GOOS == "darwin" {
		// For Darwin/ARM.
		// TODO(crawshaw): can we do better?
		cc = append(cc, []string{"-framework", "CoreFoundation", "-framework", "Foundation"}...)
	}
	libgodir = GOOS + "_" + GOARCH
	switch GOOS {
	case "darwin":
		if GOARCH == "arm" || GOARCH == "arm64" {
			libgodir += "_shared"
		}
	case "dragonfly", "freebsd", "linux", "netbsd", "openbsd", "solaris":
		libgodir += "_shared"
	}
	cc = append(cc, "-I", filepath.Join("pkg", libgodir))

	// Build an environment with GOPATH=$(pwd)
	env := os.Environ()
	var n []string
	for _, e := range env {
		if !strings.HasPrefix(e, "GOPATH=") {
			n = append(n, e)
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	n = append(n, "GOPATH="+dir)
	gopathEnv = n

	if GOOS == "windows" {
		exeSuffix = ".exe"
	}
}

func goEnv(key string) string {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go env %s failed:\n%s\n", key, err)
		if ee, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "%s", ee.Stderr)
		}
		os.Exit(2)
	}
	return strings.TrimSpace(string(out))
}

func cmdToRun(name string) []string {
	execScript := "go_" + goEnv("GOOS") + "_" + goEnv("GOARCH") + "_exec"
	executor, err := exec.LookPath(execScript)
	if err != nil {
		return []string{name}
	}
	return []string{executor, name}
}

func testInstall(t *testing.T, exe, libgoa, libgoh string, buildcmd ...string) {
	cmd := exec.Command(buildcmd[0], buildcmd[1:]...)
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
	defer func() {
		os.Remove(libgoa)
		os.Remove(libgoh)
	}()

	ccArgs := append(cc, "-o", exe, "main.c")
	if GOOS == "windows" {
		ccArgs = append(ccArgs, "main_windows.c", libgoa, "-lntdll", "-lws2_32", "-lwinmm")
	} else {
		ccArgs = append(ccArgs, "main_unix.c", libgoa)
	}
	t.Log(ccArgs)
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
	defer os.Remove(exe)

	binArgs := append(cmdToRun(exe), "arg1", "arg2")
	if out, err := exec.Command(binArgs[0], binArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
}

func TestInstall(t *testing.T) {
	defer os.RemoveAll("pkg")

	testInstall(t, "./testp1"+exeSuffix,
		filepath.Join("pkg", libgodir, "libgo.a"),
		filepath.Join("pkg", libgodir, "libgo.h"),
		"go", "install", "-buildmode=c-archive", "libgo")

	// Test building libgo other than installing it.
	// Header files are now present.
	testInstall(t, "./testp2"+exeSuffix, "libgo.a", "libgo.h",
		"go", "build", "-buildmode=c-archive", filepath.Join("src", "libgo", "libgo.go"))

	testInstall(t, "./testp3"+exeSuffix, "libgo.a", "libgo.h",
		"go", "build", "-buildmode=c-archive", "-o", "libgo.a", "libgo")
}

func TestEarlySignalHandler(t *testing.T) {
	switch GOOS {
	case "darwin":
		switch GOARCH {
		case "arm", "arm64":
			t.Skipf("skipping on %s/%s; see https://golang.org/issue/13701", GOOS, GOARCH)
		}
	case "windows":
		t.Skip("skipping signal test on Windows")
	}

	defer func() {
		os.Remove("libgo2.a")
		os.Remove("libgo2.h")
		os.Remove("testp")
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo2.a", "libgo2")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp"+exeSuffix, "main2.c", "libgo2.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	if out, err := exec.Command(bin[0], bin[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
}

func TestSignalForwarding(t *testing.T) {
	checkSignalForwardingTest(t)

	defer func() {
		os.Remove("libgo2.a")
		os.Remove("libgo2.h")
		os.Remove("testp")
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo2.a", "libgo2")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp"+exeSuffix, "main5.c", "libgo2.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	cmd = exec.Command(bin[0], append(bin[1:], "1")...)

	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	expectSignal(t, err, syscall.SIGSEGV)

	// Test SIGPIPE forwarding
	cmd = exec.Command(bin[0], append(bin[1:], "3")...)

	out, err = cmd.CombinedOutput()
	t.Logf("%s", out)
	expectSignal(t, err, syscall.SIGPIPE)
}

func TestSignalForwardingExternal(t *testing.T) {
	checkSignalForwardingTest(t)

	defer func() {
		os.Remove("libgo2.a")
		os.Remove("libgo2.h")
		os.Remove("testp")
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo2.a", "libgo2")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp"+exeSuffix, "main5.c", "libgo2.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	// We want to send the process a signal and see if it dies.
	// Normally the signal goes to the C thread, the Go signal
	// handler picks it up, sees that it is running in a C thread,
	// and the program dies. Unfortunately, occasionally the
	// signal is delivered to a Go thread, which winds up
	// discarding it because it was sent by another program and
	// there is no Go handler for it. To avoid this, run the
	// program several times in the hopes that it will eventually
	// fail.
	const tries = 20
	for i := 0; i < tries; i++ {
		cmd = exec.Command(bin[0], append(bin[1:], "2")...)

		stderr, err := cmd.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer stderr.Close()

		r := bufio.NewReader(stderr)

		err = cmd.Start()

		if err != nil {
			t.Fatal(err)
		}

		// Wait for trigger to ensure that the process is started.
		ok, err := r.ReadString('\n')

		// Verify trigger.
		if err != nil || ok != "OK\n" {
			t.Fatalf("Did not receive OK signal")
		}

		// Give the program a chance to enter the sleep function.
		time.Sleep(time.Millisecond)

		cmd.Process.Signal(syscall.SIGSEGV)

		err = cmd.Wait()

		if err == nil {
			continue
		}

		if expectSignal(t, err, syscall.SIGSEGV) {
			return
		}
	}

	t.Errorf("program succeeded unexpectedly %d times", tries)
}

// checkSignalForwardingTest calls t.Skip if the SignalForwarding test
// doesn't work on this platform.
func checkSignalForwardingTest(t *testing.T) {
	switch GOOS {
	case "darwin":
		switch GOARCH {
		case "arm", "arm64":
			t.Skipf("skipping on %s/%s; see https://golang.org/issue/13701", GOOS, GOARCH)
		}
	case "windows":
		t.Skip("skipping signal test on Windows")
	}
}

// expectSignal checks that err, the exit status of a test program,
// shows a failure due to a specific signal. Returns whether we found
// the expected signal.
func expectSignal(t *testing.T, err error, sig syscall.Signal) bool {
	if err == nil {
		t.Error("test program succeeded unexpectedly")
	} else if ee, ok := err.(*exec.ExitError); !ok {
		t.Errorf("error (%v) has type %T; expected exec.ExitError", err, err)
	} else if ws, ok := ee.Sys().(syscall.WaitStatus); !ok {
		t.Errorf("error.Sys (%v) has type %T; expected syscall.WaitStatus", ee.Sys(), ee.Sys())
	} else if !ws.Signaled() || ws.Signal() != sig {
		t.Errorf("got %v; expected signal %v", ee, sig)
	} else {
		return true
	}
	return false
}

func TestOsSignal(t *testing.T) {
	switch GOOS {
	case "windows":
		t.Skip("skipping signal test on Windows")
	}

	defer func() {
		os.Remove("libgo3.a")
		os.Remove("libgo3.h")
		os.Remove("testp")
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo3.a", "libgo3")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp"+exeSuffix, "main3.c", "libgo3.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	if out, err := exec.Command(bin[0], bin[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
}

func TestSigaltstack(t *testing.T) {
	switch GOOS {
	case "windows":
		t.Skip("skipping signal test on Windows")
	}

	defer func() {
		os.Remove("libgo4.a")
		os.Remove("libgo4.h")
		os.Remove("testp")
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo4.a", "libgo4")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp"+exeSuffix, "main4.c", "libgo4.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	if out, err := exec.Command(bin[0], bin[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
}

const testar = `#!/usr/bin/env bash
while expr $1 : '[-]' >/dev/null; do
  shift
done
echo "testar" > $1
echo "testar" > PWD/testar.ran
`

func TestExtar(t *testing.T) {
	switch GOOS {
	case "windows":
		t.Skip("skipping signal test on Windows")
	}

	defer func() {
		os.Remove("libgo4.a")
		os.Remove("libgo4.h")
		os.Remove("testar")
		os.Remove("testar.ran")
		os.RemoveAll("pkg")
	}()

	os.Remove("testar")
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := strings.Replace(testar, "PWD", dir, 1)
	if err := ioutil.WriteFile("testar", []byte(s), 0777); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-ldflags=-extar="+filepath.Join(dir, "testar"), "-o", "libgo4.a", "libgo4")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	if _, err := os.Stat("testar.ran"); err != nil {
		if os.IsNotExist(err) {
			t.Error("testar does not exist after go build")
		} else {
			t.Errorf("error checking testar: %v", err)
		}
	}
}

func TestPIE(t *testing.T) {
	switch GOOS {
	case "windows", "darwin", "plan9":
		t.Skipf("skipping PIE test on %s", GOOS)
	}

	defer func() {
		os.Remove("testp" + exeSuffix)
		os.RemoveAll("pkg")
	}()

	cmd := exec.Command("go", "install", "-buildmode=c-archive", "libgo")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-fPIE", "-pie", "-o", "testp"+exeSuffix, "main.c", "main_unix.c", filepath.Join("pkg", libgodir, "libgo.a"))
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	binArgs := append(bin, "arg1", "arg2")
	if out, err := exec.Command(binArgs[0], binArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	f, err := elf.Open("testp" + exeSuffix)
	if err != nil {
		t.Fatal("elf.Open failed: ", err)
	}
	defer f.Close()
	if hasDynTag(t, f, elf.DT_TEXTREL) {
		t.Errorf("%s has DT_TEXTREL flag", "testp"+exeSuffix)
	}
}

func hasDynTag(t *testing.T, f *elf.File, tag elf.DynTag) bool {
	ds := f.SectionByType(elf.SHT_DYNAMIC)
	if ds == nil {
		t.Error("no SHT_DYNAMIC section")
		return false
	}
	d, err := ds.Data()
	if err != nil {
		t.Errorf("can't read SHT_DYNAMIC contents: %v", err)
		return false
	}
	for len(d) > 0 {
		var t elf.DynTag
		switch f.Class {
		case elf.ELFCLASS32:
			t = elf.DynTag(f.ByteOrder.Uint32(d[:4]))
			d = d[8:]
		case elf.ELFCLASS64:
			t = elf.DynTag(f.ByteOrder.Uint64(d[:8]))
			d = d[16:]
		}
		if t == tag {
			return true
		}
	}
	return false
}

func TestSIGPROF(t *testing.T) {
	switch GOOS {
	case "windows", "plan9":
		t.Skipf("skipping SIGPROF test on %s", GOOS)
	case "darwin":
		t.Skipf("skipping SIGPROF test on %s; see https://golang.org/issue/19320", GOOS)
	}

	t.Parallel()

	defer func() {
		os.Remove("testp6" + exeSuffix)
		os.Remove("libgo6.a")
		os.Remove("libgo6.h")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-o", "libgo6.a", "libgo6")
	cmd.Env = gopathEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	ccArgs := append(cc, "-o", "testp6"+exeSuffix, "main6.c", "libgo6.a")
	if out, err := exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}

	argv := cmdToRun("./testp6")
	cmd = exec.Command(argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("%s", out)
		t.Fatal(err)
	}
}

// TestCompileWithoutShared tests that if we compile code without the
// -shared option, we can put it into an archive. When we use the go
// tool with -buildmode=c-archive, it passes -shared to the compiler,
// so we override that. The go tool doesn't work this way, but Bazel
// will likely do it in the future. And it ought to work. This test
// was added because at one time it did not work on PPC GNU/Linux.
func TestCompileWithoutShared(t *testing.T) {
	// For simplicity, reuse the signal forwarding test.
	checkSignalForwardingTest(t)

	defer func() {
		os.Remove("libgo2.a")
		os.Remove("libgo2.h")
	}()

	cmd := exec.Command("go", "build", "-buildmode=c-archive", "-gcflags=-shared=false", "-o", "libgo2.a", "libgo2")
	cmd.Env = gopathEnv
	t.Log(cmd.Args)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}

	exe := "./testnoshared" + exeSuffix
	ccArgs := append(cc, "-o", exe, "main5.c", "libgo2.a")
	t.Log(ccArgs)
	out, err = exec.Command(ccArgs[0], ccArgs[1:]...).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(exe)

	binArgs := append(cmdToRun(exe), "3")
	t.Log(binArgs)
	out, err = exec.Command(binArgs[0], binArgs[1:]...).CombinedOutput()
	t.Logf("%s", out)
	expectSignal(t, err, syscall.SIGPIPE)
}
