package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/clones"
	"github.com/a-Fig/murmur/internal/desktop"
	"github.com/a-Fig/murmur/internal/join"
)

// runApp opens the page in a window of its own (#23; docs/design/desktop.md). It starts the window
// as a process of its own and returns, so the terminal it was typed in is free and can be closed;
// --wait keeps it in the foreground.
func runApp(ctx context.Context, args []string, out io.Writer) error {
	fs, dir := flags("app")
	wait := fs.Bool("wait", false, "stay until the window closes")
	devtools := fs.Bool("devtools", false, "let the window open the web view's developer tools")
	page := fs.String("page", "", "show this address and no clone's: the devserver's (internal/web/devserver), for work on the app")
	fs.Parse(args)
	if !desktop.Supported {
		return desktop.Run(ctx, desktop.Options{})
	}
	if *page != "" {
		return desktop.Run(ctx, desktop.Options{Page: *page, Open: browse, Debug: *devtools})
	}

	cfg, err := appDir()
	if err != nil {
		return err
	}
	gitDir, err := appClone(ctx, *dir, cfg)
	if err != nil {
		return err
	}
	if !*wait {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		logf, err := os.OpenFile(filepath.Join(cfg, "app.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer logf.Close()
		args := []string{"app", "--wait", "-C", top(gitDir)}
		if *devtools {
			args = append(args, "--devtools")
		}
		if err := desktop.Detach(exe, args, logf); err != nil {
			return err
		}
		fmt.Fprintln(out, "Opened.")
		return nil
	}

	addr, err := pageOf(ctx, gitDir)
	if err != nil {
		return err
	}
	f := &follower{cfg: cfg, gitDir: gitDir, page: pageOf}
	f.remember()
	return desktop.Run(ctx, desktop.Options{
		Page:   addr,
		Follow: f.follow,
		Open:   browse,
		Debug:  *devtools,
	})
}

// appDir is the app's directory in the user's config directory, beside the clones' record: its
// log, and the clone it last showed.
func appDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, brand.Name, "app")
	return dir, os.MkdirAll(dir, 0o700)
}

// appClone is the clone the window opens on. A daemon serves one clone's page, so the app has to
// pick one, and the page's repo menu moves it to any other:
//
//  1. the clone it was started in, as `mur ui` does, if that clone joined;
//  2. else the clone it last showed (from a shortcut, the Dock, or a terminal in the home folder);
//  3. else a joined clone whose daemon is running, else any joined clone.
func appClone(ctx context.Context, dir, cfg string) (string, error) {
	if gitDir, err := client.FindGitDir(ctx, dir); err == nil && join.Joined(gitDir) {
		return gitDir, nil
	}
	record, err := clones.DefaultDir()
	if err != nil {
		return "", err
	}
	list, err := clones.List(record, join.Joined)
	if err != nil {
		return "", err
	}
	if last := lastClone(cfg); last != "" {
		for _, c := range list {
			if c.ID() == clones.ID(last) {
				return c.GitDir, nil
			}
		}
	}
	for _, c := range list {
		if client.Running(ctx, c.GitDir) {
			return c.GitDir, nil
		}
	}
	if len(list) > 0 {
		return list[0].GitDir, nil
	}
	return "", errors.New("no repo on this machine has joined " + brand.Product + ": ask an agent in one to join, or run `" + brand.Command + " join` there")
}

type lastFile struct {
	GitDir string `json:"gitDir"`
}

func lastClone(cfg string) string {
	b, err := os.ReadFile(filepath.Join(cfg, "last.json"))
	if err != nil {
		return ""
	}
	var l lastFile
	json.Unmarshal(b, &l)
	return l.GitDir
}

// follower tracks which clone's page the window shows: the page says where it is served from,
// and the page's repo menu can move it to another clone's.
type follower struct {
	cfg string
	// page starts the clone's daemon if it isn't running and returns its page's address.
	page func(ctx context.Context, gitDir string) (string, error)

	mu     sync.Mutex
	gitDir string
}

func (f *follower) remember() {
	f.mu.Lock()
	b, _ := json.Marshal(lastFile{GitDir: f.gitDir})
	f.mu.Unlock()
	p := filepath.Join(f.cfg, "last.json")
	if err := os.WriteFile(p+".tmp", b, 0o600); err == nil {
		os.Rename(p+".tmp", p)
	}
}

// follow is desktop.Options.Follow: given the host the page last reported, it returns the page
// to send the window to, or "". The clone is the one whose daemon is at that host; when none is,
// as after the daemon went, the clone stays the one it was, and its daemon is started again.
func (f *follower) follow(ctx context.Context, host string) string {
	f.mu.Lock()
	gitDir := f.gitDir
	f.mu.Unlock()
	if host != "" && client.PageHost(gitDir) != host {
		if found := cloneAt(host); found != "" && found != gitDir {
			f.mu.Lock()
			f.gitDir = found
			f.mu.Unlock()
			f.remember()
			gitDir = found
		}
	}
	if !join.Joined(gitDir) {
		return "" // left: the page says so, and nothing should start its daemon
	}
	page, err := f.page(ctx, gitDir)
	if err != nil {
		log.Printf("the page of %s: %v", gitDir, err)
		return ""
	}
	if u, err := url.Parse(page); err != nil || u.Host == host {
		return ""
	}
	return page
}

func pageOf(ctx context.Context, gitDir string) (string, error) {
	c, err := client.Connect(ctx, gitDir, true)
	if err != nil {
		return "", err
	}
	return c.PageURL()
}

// cloneAt is the joined clone whose daemon serves its page at host, or "".
func cloneAt(host string) string {
	record, err := clones.DefaultDir()
	if err != nil {
		return ""
	}
	list, _ := clones.List(record, join.Joined)
	for _, c := range list {
		if client.PageHost(c.GitDir) == host {
			return c.GitDir
		}
	}
	return ""
}

// top is a git common dir's working tree, where -C can point.
func top(gitDir string) string {
	return clones.Clone{GitDir: gitDir}.Top()
}
