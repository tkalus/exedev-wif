package main_test

//nolint:depguard,importas // External tests intentionally import the command package via export_test wrappers.
import (
	"bytes"
	"errors"
	"strings"
	"testing"

	exedev "tkalus.dev/exedev-wif"
)

const (
	helpArg  = "--help"
	extraArg = "extra"
	getCmd   = "get"
	setupCmd = "setup"
)

func TestRunWithOutputWritesRootHelpWithoutArgs(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput(nil, &stdout)
	if err != nil {
		t.Fatalf("run with no args: %v", err)
	}

	assertContains(t, stdout.String(), "usage: exedev-wif [--help] <command>")
	assertContains(t, stdout.String(), "serve")
	assertContains(t, stdout.String(), "get")
	assertContains(t, stdout.String(), "setup")
}

func TestRunWithOutputWritesRootHelpForHelpAliases(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"-h"}, {helpArg}, {"help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer

			err := exedev.ExportTestRunWithOutput(args, &stdout)
			if err != nil {
				t.Fatalf("run help: %v", err)
			}

			assertContains(t, stdout.String(), "usage: exedev-wif [--help] <command>")
		})
	}
}

func TestRunWithOutputReturnsUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{"wat"}, &stdout)
	if !errors.Is(err, exedev.ExportTestErrUnknownCommand()) {
		t.Fatalf("error = %v, want unknown command", err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunWithOutputServeHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{"serve", helpArg}, &stdout)
	if err != nil {
		t.Fatalf("run serve help: %v", err)
	}

	assertContains(t, stdout.String(), "usage: exedev-wif serve")
	assertContains(t, stdout.String(), "--socket PATH")
	assertContains(t, stdout.String(), "--pidfile PATH")
}

func TestRunWithOutputServeRejectsPositionalArgs(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{"serve", extraArg}, &stdout)
	if !errors.Is(err, exedev.ExportTestErrServePositional()) {
		t.Fatalf("error = %v, want serve positional", err)
	}
}

func TestRunWithOutputGetHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{getCmd, helpArg}, &stdout)
	if err != nil {
		t.Fatalf("run get help: %v", err)
	}

	assertContains(t, stdout.String(), "usage: exedev-wif get")
	assertContains(t, stdout.String(), "/aws/INTEGRATION")
}

func TestRunWithOutputGetRejectsWrongArgumentCount(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{getCmd}, {getCmd, "/aws/default", extraArg}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer

			err := exedev.ExportTestRunWithOutput(args, &stdout)
			if !errors.Is(err, exedev.ExportTestErrGetUsage()) {
				t.Fatalf("error = %v, want get usage", err)
			}
		})
	}
}

func TestRunWithOutputSetupHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{setupCmd, helpArg}, &stdout)
	if err != nil {
		t.Fatalf("run setup help: %v", err)
	}

	assertContains(t, stdout.String(), "usage: exedev-wif setup")
	assertContains(t, stdout.String(), "--integration NAME")
}

func TestRunWithOutputSetupRejectsPositionalArgs(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput([]string{setupCmd, extraArg}, &stdout)
	if !errors.Is(err, exedev.ExportTestErrSetupPositional()) {
		t.Fatalf("error = %v, want setup positional", err)
	}
}

func TestRunWithOutputSetupRendersScriptWithSocketAndIntegration(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	err := exedev.ExportTestRunWithOutput(
		[]string{setupCmd, "--socket", "/tmp/exedev.sock", "--integration", "prod"},
		&stdout,
	)
	if err != nil {
		t.Fatalf("run setup: %v", err)
	}

	output := stdout.String()
	assertContains(t, output, "socket_path='/tmp/exedev.sock'")
	assertContains(t, output, "integration='prod'")
	assertContains(t, output, "credential_process =")
	assertContains(t, output, "/aws/$integration")
}

func TestWantsHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short", args: []string{"-h"}, want: true},
		{name: "long", args: []string{helpArg}, want: true},
		{name: "word", args: []string{"help"}, want: true},
		{name: "none", args: nil, want: false},
		{name: "with other args", args: []string{helpArg, extraArg}, want: false},
		{name: "unknown", args: []string{"-x"}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := exedev.ExportTestWantsHelp(test.args)
			if got != test.want {
				t.Fatalf("wantsHelp(%v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "plain", value: "abc", want: "'abc'"},
		{name: "empty", value: "", want: "''"},
		{name: "single quote", value: "can't", want: "'can'\\''t'"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := exedev.ExportTestShellQuote(test.value)
			if got != test.want {
				t.Fatalf("shellQuote(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func assertContains(t *testing.T, value string, substring string) {
	t.Helper()

	if !strings.Contains(value, substring) {
		t.Fatalf("%q does not contain %q", value, substring)
	}
}
