//go:build windows

package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The test binary re-executes itself in these modes, so a test can start a process in a job object
// the way an agent's MCP server would, without a second program to build.
const (
	helperStart = "proc-helper-start" // args: log path, PowerShell path ("" for the real one)
	helperSleep = "proc-helper-sleep" // args: optionally --log FILE, as a WMI start passes it
	readyMarker = "proc helper: ready"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case helperStart:
			helperStartMain(os.Args[2:])
		case helperSleep:
			helperSleepMain(os.Args[2:])
		}
	}
	os.Exit(m.Run())
}

// helperStartMain stands in for an agent's MCP server. It puts itself in a job that kills its
// members when the job closes and forbids breakaway, starts a sleeper with StartDetached, prints
// the sleeper's PID, and closes the job. Closing the job ends this process too.
func helperStartMain(args []string) {
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(args) != 2 {
		fail(errors.New("usage: proc-helper-start LOG PSPATH"))
	}
	logPath, psExe := args[0], args[1]

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		fail(err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		fail(err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		fail(err)
	}
	if psExe != "" {
		powershellPath = psExe
	}

	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	p, err := StartDetached(exe, []string{helperSleep}, filepath.Dir(logPath), logf)
	if err != nil {
		fail(err)
	}
	fmt.Println(p.Pid)
	windows.CloseHandle(job)
	time.Sleep(time.Minute) // not reached: the job's close ends this process
	os.Exit(0)
}

// helperSleepMain stands in for the daemon. Like the daemon, it takes --log FILE and writes there.
func helperSleepMain(args []string) {
	if len(args) == 2 && args[0] == "--log" {
		f, err := os.OpenFile(args[1], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			os.Exit(1)
		}
		os.Stdout, os.Stderr = f, f
	}
	fmt.Println(readyMarker)
	time.Sleep(10 * time.Minute)
	os.Exit(0)
}

// TestStartDetachedSurvivesJobClose is the reproduction for #28. A daemon started from a job that
// forbids breakaway must outlive that job, and its output must still reach the log file.
func TestStartDetachedSurvivesJobClose(t *testing.T) {
	pid, logPath := startFromJobHelper(t, "")
	time.Sleep(time.Second)
	if !alive(pid) {
		t.Fatalf("daemon %d died when the job that started it closed; log:\n%s", pid, readFile(logPath))
	}
	waitForLog(t, logPath, readyMarker)
}

// TestStartDetachedFallsBackInsideJob checks the last step of the order: with WMI blocked too, the
// daemon starts inside the job, as it did before WMI was tried, and says so in the log.
func TestStartDetachedFallsBackInsideJob(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-powershell.exe")
	pid, logPath := startFromJobHelper(t, missing)
	if !strings.Contains(readFile(logPath), "inside the job") {
		t.Fatalf("log doesn't say the daemon started inside the job:\n%s", readFile(logPath))
	}
	if !waitUntil(5*time.Second, func() bool { return !alive(pid) }) {
		t.Fatalf("daemon %d survived a job close, but it started inside the job", pid)
	}
}

// TestStartDetachedWithoutJob checks the ordinary case: no job, so breakaway succeeds and the
// output goes to the log file.
func TestStartDetachedWithoutJob(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logf.Close() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := StartDetached(exe, []string{helperSleep}, filepath.Dir(logPath), logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		kill(p.Pid)
		p.Release()
	})
	waitForLog(t, logPath, readyMarker)
}

// TestWindowsCommandLineRoundTrip checks that the command line handed to WMI splits back into the
// same argv, including a path with spaces (the Desktop path has them) and awkward quoting.
func TestWindowsCommandLineRoundTrip(t *testing.T) {
	path := `C:\Users\smash\Desktop\Claude Work Space\t4\t4.exe`
	args := []string{
		"daemon", "--git-dir", `C:\Users\smash\Desktop\Claude Work Space\t4\.git`,
		"--log", `C:\dir with "quotes"\ends with a slash\`,
	}
	got, err := windows.DecomposeCommandLine(windowsCommandLine(path, args))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]string{path}, args...)
	if len(got) != len(want) {
		t.Fatalf("got %d arguments %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argument %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// startFromJobHelper runs the helper and returns the sleeper's PID and the log it writes. The
// helper's job has closed by the time this returns, so the sleeper is alive only if it escaped.
func startFromJobHelper(t *testing.T, psExe string) (int, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "daemon.log")
	exe := copyTestBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, helperStart, logPath, psExe).Output()
	if err != nil {
		var ee *exec.ExitError
		stderr := ""
		if errors.As(err, &ee) {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("helper: %v\n%s\nlog:\n%s", err, stderr, readFile(logPath))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("helper printed %q, not a PID: %v", out, err)
	}
	t.Cleanup(func() { kill(pid) })
	return pid, logPath
}

// copyTestBinary copies this test binary into a directory whose path has a space, so every start
// here quotes a path with a space in it, as the Desktop path does.
func copyTestBinary(t *testing.T) string {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "with space", "proc.test.exe")
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return dst
}

// alive reports whether the process with pid is still running.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// kill ends the process with pid, if it still exists, and waits for it to go, so the test's
// temporary directory can be removed afterwards.
func kill(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	windows.TerminateProcess(h, 1)
	windows.WaitForSingleObject(h, 5000)
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cond()
}

func waitForLog(t *testing.T, path, marker string) {
	t.Helper()
	if !waitUntil(15*time.Second, func() bool { return strings.Contains(readFile(path), marker) }) {
		t.Fatalf("log never got %q:\n%s", marker, readFile(path))
	}
}
