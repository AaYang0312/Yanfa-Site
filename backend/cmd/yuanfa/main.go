// Command yuanfa owns the only long-lived application process. All writable
// commands use the same permanent DATA_DIR flock before opening SQLite.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
)

const help = `Usage: yuanfa <command>

Commands:
  migrate                     Apply checksum-verified SQL migrations atomically.
  init-admin --username admin  Create the single admin; password is prompted on a
                              TTY with echo disabled, then confirmed (12-1024 chars).
  import-seed --seed FILE --media-root DIR
                              Initialize an empty workspace and private images.
                              Same seed is a no-op; never overwrites later edits.
  serve                       Serve HTTP until SIGINT/SIGTERM; drain HTTP and
                              workers, close SQLite, then release DATA_DIR.

Every command holds DATA_DIR/.yuanfa-data.lock using Linux flock(EX|NB).
Never delete/replace this lock file. Conflicts: DATA_DIR_LOCKED, exit 73.
Incomplete restores (.restore-incomplete) must be completed before startup.
No self-registration, password argument, password environment variable or reset.

Environment:
  SITE_URL=http://127.0.0.1:8080  Exact browser origin; HTTPS except loopback dev.
  DATA_DIR=./data               Persistent data; canonicalized before SQLite.
  HTTP_ADDR=127.0.0.1:8080      HTTP listen address.
  TRUSTED_PROXIES=              Comma-separated IPs/CIDRs; otherwise ignore XFF.
  MODEL_PROVIDER=openai        MODEL_BASE_URL / MODEL_MODEL / MODEL_API_KEY.
  PURCHASE_ALLOWED_HOSTS=      Comma-separated exact purchase hostnames.
  GUIDE_IP_RPM=10              GUIDE_MAX_CONCURRENCY=4.
  BUILD_TIMEOUT=300s           BUILD_NODE_HEAP_MB=512.
  TEMPLATE_DIR=.               Fixed build template; never selected by requests.
  ADMIN_STATIC_DIR=./admin/dist
  SITE_CONFIG_PATH=./src/generated/site-config.json

Model configuration is optional. Serve loads and verifies state/active.json;
before the first confirmed publication the website and readiness return 503.
TEMPLATE_DIR must contain the integrated build-candidate.mjs and installed Node22
dependencies. Builds use private work/cache/tmp directories, a 300s maximum,
bounded Node heap and Linux resource limits; no npm install or Git fetch occurs.
Only a browser administrator may confirm a publication or rollback. Rollback
changes the public pointer and preserves the complete editable workspace.
Passwords, session secrets and Token values are never logged.
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func run(args []string, in *os.File, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	username := ""
	seedPath, mediaRoot := "", ""
	switch command {
	case "init-admin":
		flags.StringVar(&username, "username", "admin", "administrator username")
	case "import-seed":
		flags.StringVar(&seedPath, "seed", "", "read-only catalog snapshot")
		flags.StringVar(&mediaRoot, "media-root", "", "seed image root")
	case "migrate", "serve":
	default:
		fmt.Fprintln(errOut, "UNKNOWN_COMMAND: use yuanfa help")
		return 2
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "INVALID_ARGUMENT: unexpected positional argument")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(errOut, "CONFIG_INVALID:", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	application, err := app.Open(ctx, cfg)
	if err != nil {
		return fail(errOut, err)
	}
	var actionErr error
	switch command {
	case "migrate":
		fmt.Fprintln(out, "MIGRATIONS_APPLIED")
	case "init-admin":
		var password string
		password, actionErr = readPassword(ctx, in, out)
		if actionErr == nil {
			actionErr = application.Auth.InitAdmin(ctx, username, password)
		}
		password = ""
		if actionErr == nil {
			fmt.Fprintln(out, "ADMIN_INITIALIZED")
		}
	case "import-seed":
		actionErr = application.Content.ImportSeed(ctx, seedPath, mediaRoot)
		if actionErr == nil {
			fmt.Fprintln(out, "SEED_IMPORTED")
		}
	case "serve":
		fmt.Fprintln(out, "HTTP_STARTING")
		actionErr = application.Serve()
	}
	actionErr = errors.Join(actionErr, application.Close())
	if actionErr != nil {
		return fail(errOut, actionErr)
	}
	return 0
}
func fail(out io.Writer, err error) int {
	if errors.Is(err, coordination.ErrDataDirLocked) {
		fmt.Fprintln(out, "DATA_DIR_LOCKED")
		return 73
	}
	if errors.Is(err, app.ErrRestoreIncomplete) {
		fmt.Fprintln(out, "RESTORE_INCOMPLETE")
		return 1
	}
	if errors.Is(err, errTTYRequired) {
		fmt.Fprintln(out, errTTYRequired)
		return 1
	}
	var business *contract.BusinessError
	if errors.As(err, &business) {
		fmt.Fprintln(out, business.Detail.Code+": "+business.Detail.Message)
		return 1
	}
	// Detailed SQL/path errors can contain sensitive data; do not print causes.
	fmt.Fprintln(out, "OPERATION_FAILED: unable to complete command")
	return 1
}

var errTTYRequired = errors.New("TTY_REQUIRED: run init-admin in an interactive TTY")

// readPassword uses the controlling terminal; redirected stdin is rejected.
// Termios is restored on success, input errors and SIGINT/SIGTERM cancellation.
func readPassword(ctx context.Context, in *os.File, out io.Writer) (string, error) {
	fd := in.Fd()
	var original syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&original))); errno != 0 {
		return "", errTTYRequired
	}
	hidden := original
	hidden.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&hidden))); errno != 0 {
		return "", errno
	}
	defer syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&original)))
	fmt.Fprint(out, "Password: ")
	first, err := terminalLine(ctx, int(fd))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	fmt.Fprint(out, "Confirm password: ")
	second, err := terminalLine(ctx, int(fd))
	fmt.Fprintln(out)
	if err != nil {
		return "", err
	}
	if first != second {
		return "", contract.NewBusinessError(422, "PASSWORD_MISMATCH", "两次密码不一致。")
	}
	return first, nil
}

func terminalLine(ctx context.Context, fd int) (string, error) {
	if fd < 0 || fd >= 1024 {
		return "", errTTYRequired
	}
	var data []byte
	buffer := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var ready syscall.FdSet
		ready.Bits[fd/64] |= 1 << uint(fd%64)
		timeout := syscall.Timeval{Usec: 100000}
		n, err := syscall.Select(fd+1, &ready, nil, nil, &timeout)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}
		n, err = syscall.Read(fd, buffer)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "", io.EOF
		}
		data = append(data, buffer[:n]...)
		if len(data) > 4097 {
			return "", contract.NewBusinessError(422, "VALIDATION_FAILED", "密码过长。", "password")
		}
		if strings.HasSuffix(string(data), "\n") {
			return strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"), nil
		}
	}
}
