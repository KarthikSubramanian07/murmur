package proc

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive lock on path, held until the file is closed or the process exits,
// however it exits.
func Lock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var ol windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return f, nil
}

const (
	createNoWindow         = 0x08000000
	createBreakawayFromJob = 0x01000000
)

// wmiTimeout bounds the PowerShell call that asks WMI to start the daemon.
const wmiTimeout = 30 * time.Second

// powershellPath runs the WMI call. A variable so a test can point it at nothing.
var powershellPath = filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")

// findProcess turns a PID WMI returned into a handle. A variable so a test can make it fail the way
// it does for a process that has already exited.
var findProcess = os.FindProcess

// wmiStartScript asks WMI to create the process named by T4_WMI_COMMAND, in the directory named by
// T4_WMI_DIR, with the environment named by T4_WMI_ENV, and prints its PID. WMI creates it outside
// the caller's job object (the research for #19 found it survives a job that forbids breakaway).
// ShowWindow 0 keeps any console hidden. EnvironmentVariables replaces the environment rather than
// adding to it, so the whole caller environment is passed.
const wmiStartScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$environment = [string[]]@($env:T4_WMI_ENV -split ',' | Where-Object { $_ } | ForEach-Object { [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($_)) })
$startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0; EnvironmentVariables = $environment }
$createArgs = @{ CommandLine = $env:T4_WMI_COMMAND; ProcessStartupInformation = $startup }
if ($env:T4_WMI_DIR) { $createArgs.CurrentDirectory = $env:T4_WMI_DIR }
$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments $createArgs
if ($r.ReturnValue -ne 0) { throw ('Win32_Process.Create returned ' + $r.ReturnValue) }
[Console]::Out.Write($r.ProcessId)`

// StartDetached starts a program with a hidden console, in its own process group, and outside
// the starter's job object where allowed. Agent clients built on libuv put their children in a
// job that is killed when the client exits; breaking away keeps the daemon alive for the
// client's other sessions. It tries three ways, in order, and logs to out which one worked:
//   - breakaway, where the job allows it;
//   - WMI, where it doesn't. WMI gives the new process no handles, so the daemon is told the
//     log's path with --log and opens the file itself;
//   - inside the job, if WMI is blocked too. The daemon then dies with the job, and the next
//     session to need a daemon starts another.
//
// If WMI created the process and it had already exited by the time it was looked up, StartDetached
// returns a nil process and no error: the start worked, and there is nothing to hold.
func StartDetached(path string, args []string, dir string, out *os.File) (*os.Process, error) {
	logger := log.New(out, "", log.LstdFlags)
	flags := uint32(createNoWindow | syscall.CREATE_NEW_PROCESS_GROUP)
	p, err := startWith(path, args, dir, out, flags|createBreakawayFromJob)
	if err == nil {
		logger.Printf("daemon started outside the job, by breakaway (pid %d)", p.Pid)
		return p, nil
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, err
	}
	pid, wmiErr := startThroughWMI(path, args, dir, out)
	if wmiErr == nil {
		p, err := findProcess(pid)
		if err != nil {
			logger.Printf("daemon started through WMI (pid %d) but had already exited: %v", pid, err)
			return nil, nil
		}
		logger.Printf("daemon started outside the job, through WMI (pid %d)", pid)
		return p, nil
	}
	logger.Printf("breakaway denied and WMI failed (%v); starting the daemon inside the job", wmiErr)
	p, err = startWith(path, args, dir, out, flags)
	if err == nil {
		logger.Printf("daemon started inside the job (pid %d)", p.Pid)
	}
	return p, err
}

func startWith(path string, args []string, dir string, out *os.File, flags uint32) (*os.Process, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// startThroughWMI starts the program with WMI and returns its PID. Its output goes to out's file,
// named to the program as --log, since WMI can't hand over out's handle. WMI resolves a relative
// path or directory against its own working directory, not the caller's, so both are made absolute
// here.
func startThroughWMI(path string, args []string, dir string, out *os.File) (int, error) {
	exe, err := wmiProgram(path)
	if err != nil {
		return 0, err
	}
	cwd, err := wmiDir(dir)
	if err != nil {
		return 0, err
	}
	logPath, err := filepath.Abs(out.Name())
	if err != nil {
		return 0, err
	}
	withLog := append(append([]string{}, args...), "--log", logPath)
	return createThroughWMI(windowsCommandLine(exe, withLog), cwd, os.Environ())
}

// wmiProgram returns the absolute path of the program. A bare name is looked up on PATH, as
// exec.Command would look it up.
func wmiProgram(path string) (string, error) {
	if !strings.ContainsAny(path, `\/`) {
		if found, err := exec.LookPath(path); err == nil {
			path = found
		}
	}
	return filepath.Abs(path)
}

// wmiDir returns the absolute working directory. An empty dir means the caller's, as it does for
// exec.Cmd, and WMI would otherwise use C:\Windows\system32.
func wmiDir(dir string) (string, error) {
	if dir == "" {
		return os.Getwd()
	}
	return filepath.Abs(dir)
}

// createThroughWMI runs wmiStartScript in Windows PowerShell. The command line, directory and
// environment go through environment variables, so no quoting has to survive between Go and
// PowerShell. Those variables are the only way the script gets its input.
func createThroughWMI(cmdline, dir string, env []string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wmiTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath, "-NoProfile", "-NonInteractive",
		"-EncodedCommand", encodePowerShell(wmiStartScript))
	cmd.Env = append(os.Environ(), "T4_WMI_COMMAND="+cmdline, "T4_WMI_DIR="+dir,
		"T4_WMI_ENV="+encodeEnv(env))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return 0, err
	}
	return parseWMIPID(string(out))
}

// parseWMIPID reads the PID the script printed. WMI's PID 0 is not a process, so it is an error.
func parseWMIPID(out string) (int, error) {
	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("WMI's reply %q is not a PID", out)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("WMI returned PID %d", pid)
	}
	return pid, nil
}

// encodeEnv packs environment entries for T4_WMI_ENV: each entry base64-encoded, so an entry's
// quotes, spaces and non-ASCII letters survive, and the entries separated by commas, which base64
// never contains.
func encodeEnv(env []string) string {
	parts := make([]string, len(env))
	for i, kv := range env {
		parts[i] = base64.StdEncoding.EncodeToString([]byte(kv))
	}
	return strings.Join(parts, ",")
}

// encodePowerShell is the -EncodedCommand form of a script: UTF-16LE, base64. It is used so the
// script's quotes and dollar signs reach PowerShell untouched.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		b[2*i] = byte(u)
		b[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// windowsCommandLine joins a program and its arguments into one command line that the new
// process's C runtime splits back into the same argv. Arguments with spaces or quotes are escaped.
func windowsCommandLine(path string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	for _, a := range append([]string{path}, args...) {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return strings.Join(parts, " ")
}
