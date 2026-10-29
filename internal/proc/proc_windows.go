package proc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
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

// wmiStartScript asks WMI to create the process named by T4_WMI_COMMAND, in the directory named by
// T4_WMI_DIR, and prints its PID. WMI creates it outside the caller's job object (the research for
// #19 found it survives a job that forbids breakaway). ShowWindow 0 keeps any console hidden.
const wmiStartScript = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
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
		logger.Printf("daemon started outside the job, through WMI (pid %d)", pid)
		return os.FindProcess(pid)
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
// named to the program as --log, since WMI can't hand over out's handle.
func startThroughWMI(path string, args []string, dir string, out *os.File) (int, error) {
	exe, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	logPath, err := filepath.Abs(out.Name())
	if err != nil {
		return 0, err
	}
	withLog := append(append([]string{}, args...), "--log", logPath)
	return createThroughWMI(windowsCommandLine(exe, withLog), dir)
}

// createThroughWMI runs wmiStartScript in Windows PowerShell. The command line and directory go
// through the environment, so no quoting has to survive between Go and PowerShell.
func createThroughWMI(cmdline, dir string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wmiTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, powershellPath, "-NoProfile", "-NonInteractive",
		"-EncodedCommand", encodePowerShell(wmiStartScript))
	cmd.Env = append(os.Environ(), "T4_WMI_COMMAND="+cmdline, "T4_WMI_DIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// encodePowerShell is the -EncodedCommand form of a script: UTF-16LE, base64. It is used so the
// script's quotes and dollar signs reach PowerShell untouched.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[2*i:], u)
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
