package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"tkalus.dev/mostly-harmless/httpunixagent"
)

const commandTimeout = 30 * time.Second

const errorBodyLimit = 4096

var (
	errUnknownCommand  = errors.New("unknown command")
	errServePositional = errors.New("serve takes no positional arguments")
	errGetUsage        = errors.New("usage: exedev-wif get [--socket PATH] /aws/INTEGRATION")
	errSetupPositional = errors.New("setup takes no positional arguments")
	errServerStatus    = errors.New("server returned unsuccessful status")
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	err := run(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}
}

func run(args []string) error {
	return runWithOutput(args, os.Stdout)
}

func runWithOutput(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		writeRootHelp(stdout)

		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		writeRootHelp(stdout)

		return nil
	case "serve":
		return runServe(args[1:], stdout)
	case "get":
		return runGet(args[1:], stdout)
	case "setup":
		return runSetup(args[1:], stdout)
	default:
		return fmt.Errorf("%w: %q", errUnknownCommand, args[0])
	}
}

func runServe(args []string, stdout io.Writer) error {
	if wantsHelp(args) {
		writeServeHelp(stdout)

		return nil
	}

	defaultSocket, defaultPIDFile := httpunixagent.RuntimePaths("exedev-wif")
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	socketPath := flags.String("socket", defaultSocket, "Unix socket path")
	pidFilePath := flags.String("pidfile", defaultPIDFile, "PID file path")

	err := flags.Parse(args)
	if err != nil {
		return fmt.Errorf("parse serve flags: %w", err)
	}

	if flags.NArg() != 0 {
		return errServePositional
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	awsConfig, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{provider}/{integration}", NewExeDev(awsConfig).Handle)

	err = httpunixagent.ServeBackground(ctx, httpunixagent.BackgroundServerConfig{
		SocketPath:           *socketPath,
		PIDFilePath:          *pidFilePath,
		Handler:              mux,
		ReadHeaderTimeout:    0,
		ShutdownTimeout:      0,
		SocketMode:           0,
		RuntimeDirectoryMode: 0,
	})
	if err != nil {
		return fmt.Errorf("serve background agent: %w", err)
	}

	return nil
}

func runGet(args []string, stdout io.Writer) error {
	if wantsHelp(args) {
		writeGetHelp(stdout)

		return nil
	}

	defaultSocket, _ := httpunixagent.RuntimePaths("exedev-wif")
	flags := flag.NewFlagSet("get", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	socketPath := flags.String("socket", defaultSocket, "Unix socket path")

	err := flags.Parse(args)
	if err != nil {
		return fmt.Errorf("parse get flags: %w", err)
	}

	if flags.NArg() != 1 {
		return errGetUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	agent, err := httpunixagent.NewBackground(httpunixagent.BackgroundConfig{
		SocketPath:   *socketPath,
		PIDFilePath:  "",
		AutoStart:    false,
		StartCommand: nil,
		StartTimeout: 0,
		ProbePath:    "",
	})
	if err != nil {
		return fmt.Errorf("create background agent: %w", err)
	}

	err = agent.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("connect to exedev-wif server at %q: %w", *socketPath, err)
	}

	return fetchCredentials(ctx, agent, flags.Arg(0), stdout)
}

func fetchCredentials(ctx context.Context, agent *httpunixagent.Agent, path string, stdout io.Writer) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, agent.URL(path), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	response, err := agent.Client().Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, errorBodyLimit))

		return fmt.Errorf("%w: %s: %s", errServerStatus, response.Status, body)
	}

	_, err = io.Copy(stdout, response.Body)
	if err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	return nil
}

func runSetup(args []string, stdout io.Writer) error {
	if wantsHelp(args) {
		writeSetupHelp(stdout)

		return nil
	}

	defaultSocket, _ := httpunixagent.RuntimePaths("exedev-wif")
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	socketPath := flags.String("socket", defaultSocket, "Unix socket path")
	integration := flags.String("integration", "default", "exe.dev integration name")

	err := flags.Parse(args)
	if err != nil {
		return fmt.Errorf("parse setup flags: %w", err)
	}

	if flags.NArg() != 0 {
		return errSetupPositional
	}

	executable, err := installedExecutable()
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(
		stdout,
		setupScript,
		shellQuote(executable),
		shellQuote(*socketPath),
		shellQuote(*integration),
	)
	if err != nil {
		return fmt.Errorf("write setup script: %w", err)
	}

	return nil
}

func installedExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}

	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable %q: %w", executable, err)
	}

	return executable, nil
}

func wantsHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeRootHelp(writer io.Writer) {
	_, _ = io.WriteString(writer, `usage: exedev-wif [--help] <command>

Commands:
  serve   Run the local credential agent server.
  get     Fetch AWS credential_process credentials from the agent.
  setup   Print a setup script for systemd user service and AWS config.

Run "exedev-wif <command> --help" for command details.
`)
}

func writeServeHelp(writer io.Writer) {
	_, _ = io.WriteString(writer, `usage: exedev-wif serve [--socket PATH] [--pidfile PATH]

Run the local exedev-wif HTTP agent over a Unix socket.

Options:
  --socket PATH   Unix socket path. Defaults under XDG_RUNTIME_DIR or /tmp.
  --pidfile PATH  PID file path. Defaults next to the socket.
`)
}

func writeGetHelp(writer io.Writer) {
	_, _ = io.WriteString(writer, `usage: exedev-wif get [--socket PATH] /aws/INTEGRATION

Fetch AWS credential_process JSON from the local exedev-wif agent.

Options:
  --socket PATH   Unix socket path. Defaults under XDG_RUNTIME_DIR or /tmp.
`)
}

func writeSetupHelp(writer io.Writer) {
	_, _ = io.WriteString(writer, `usage: exedev-wif setup [--socket PATH] [--integration NAME]

Print a shell script that can be piped to bash:

  exedev-wif setup | bash

The script installs a systemd user service for:

  exedev-wif serve --socket PATH

It also writes ~/.aws/config so the default AWS profile uses credential_process
to call this installed exedev-wif binary with:

  exedev-wif get --socket PATH /aws/NAME

Options:
  --socket PATH       Unix socket path. Defaults under XDG_RUNTIME_DIR or /tmp.
  --integration NAME  exe.dev integration name for /aws/NAME. Default: default.
`)
}

const setupScript = `#!/usr/bin/env bash
set -euo pipefail

exedev_wif=%s
socket_path=%s
integration=%s
service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
service_file="$service_dir/exedev-wif.service"
aws_dir="$HOME/.aws"
aws_config="$aws_dir/config"

mkdir -p "$service_dir" "$aws_dir"

cat >"$service_file" <<SERVICE
[Unit]
Description=exe.dev AWS WIF credential agent

[Service]
ExecStart=$exedev_wif serve --socket $socket_path
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=default.target
SERVICE

if [[ -e "$aws_config" ]]; then
  cp "$aws_config" "$aws_config.bak.$(date +%%Y%%m%%d%%H%%M%%S)"
fi

cat >"$aws_config" <<AWS_CONFIG
[default]
credential_process = $exedev_wif get --socket $socket_path /aws/$integration
AWS_CONFIG

systemctl --user daemon-reload
systemctl --user enable exedev-wif.service
systemctl --user start exedev-wif.service

printf 'Installed %%s\n' "$service_file"
printf 'Wrote %%s\n' "$aws_config"
`
