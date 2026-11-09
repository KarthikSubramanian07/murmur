package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/join"
)

// runUI opens the page for humans: the daemon serves it, and starts if it isn't running. The
// address carries the page's token in its fragment, so whoever has the address has the page: what
// it shows, this device's settings and session tags. It is not the token sessions use, which lets
// its holder speak for any of this device's sessions, so a leak through the browser's history or
// an extension doesn't give that (#42).
func runUI(ctx context.Context, args []string, out io.Writer, open func(url string) error) error {
	fs, dir := flags("ui")
	noOpen := fs.Bool("no-open", false, "only print the page's address; don't open a browser")
	fs.Parse(args)

	gitDir, err := client.FindGitDir(ctx, *dir)
	if err != nil {
		return err
	}
	if !join.Joined(gitDir) {
		return fmt.Errorf("this clone hasn't joined %s, and the page is the daemon's, which runs only for a user who has: "+
			"ask the agent in this repo to join, or run `%s join`", brand.Product, brand.Command)
	}
	c, err := client.Connect(ctx, gitDir, true)
	if err != nil {
		return err
	}
	url, err := c.PageURL()
	if errors.Is(err, client.ErrNoPage) {
		return fmt.Errorf("the daemon isn't serving the page. If it was started by an older version, "+
			"stop it with `%s stop` and run this again; if not, the reason is in %s",
			brand.Command, filepath.Join(client.StateDir(gitDir), "daemon.log"))
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, url)
	if *noOpen {
		return nil
	}
	if err := open(url); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser (%v); open the address above\n", err)
	}
	return nil
}

// browse opens url in the default browser. It doesn't wait for the browser to exit, only to see
// that the program that hands the address to it has not failed.
func browse(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		return nil
	}
}
