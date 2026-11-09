package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/here"
	"github.com/a-Fig/murmur/internal/join"
)

// moveWait is how long join waits for the session's server to move; it looks every 2 s.
var moveWait = 10 * time.Second

// bringCaller moves the session that ran join into the repo it joined, when its MCP server started
// somewhere else: in a parent folder, the home directory, or another repo (#200). A session is in
// one repo at a time, so one that was on in another leaves it. A person running join from a
// terminal has no session, and nothing happens.
func bringCaller(ctx context.Context, dir string, r *join.Report) {
	hdir, err := here.DefaultDir()
	if err != nil {
		return
	}
	p, err := join.Locate(ctx, dir)
	if err != nil {
		return
	}
	workDir, err := filepath.Abs(dir)
	if err != nil {
		return
	}
	sessions, err := here.Sessions(hdir)
	if err != nil || len(sessions) == 0 {
		return
	}
	claudePID, _ := strconv.Atoi(os.Getenv("CLAUDE_PID"))
	var claude string
	if os.Getenv("CLAUDECODE") == "1" {
		claude = os.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	s, ok, why := pickMove(here.Caller(sessions, here.Ancestors(), claudePID, claude))
	if why != "" {
		r.Notes = append(r.Notes, why)
	}
	if !ok {
		return
	}
	if s.GitDir == p.GitDir {
		return // it started in this clone, and turns on by itself now that the user joined
	}
	if err := here.Ask(hdir, s, workDir, time.Now()); err != nil {
		r.Notes = append(r.Notes, "Moving this session into "+brand.Product+" here: "+err.Error())
		return
	}
	from := "It started outside this repo"
	if s.On {
		from = "It was in " + brand.Product + " in " + repoOf(s.GitDir) + " and has left it there, since a session is in one repo at a time"
	}
	for end := time.Now().Add(moveWait); time.Now().Before(end); time.Sleep(250 * time.Millisecond) {
		now, _ := here.Sessions(hdir)
		for _, n := range now {
			if n.ServerPID == s.ServerPID && n.GitDir == p.GitDir && n.On {
				if len(r.Did) == 1 && strings.HasPrefix(r.Did[0], "Nothing to do") {
					r.Did = nil // the clone had joined, but this session hadn't
				}
				r.Did = append(r.Did, "Moved this session into "+brand.Product+" here. "+from+
					". Its tools work now, and `who` shows its name, which teammates' agents reach it by")
				return
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	r.Notes = append(r.Notes, fmt.Sprintf("Moving this session into %s here: it was asked to, and hadn't within %s. "+
		"It may still; if `who` doesn't work in a minute, start a session in this repo.", brand.Product, moveWait))
}

// pickMove is the session to move, of the servers Caller found for the command, or why none is
// moved ("" when there is no session at all, as for a person running join in a terminal).
func pickMove(mine []here.Session) (here.Session, bool, string) {
	for _, s := range mine {
		if s.Fixed {
			return here.Session{}, false, "Moving this session into " + brand.Product + " here: its agent (" + s.Agent + ") runs one " + brand.Product +
				" server for all its sessions in a folder, so it can't be moved for one of them. A session that starts in this repo is in " +
				brand.Product + " from the start."
		}
	}
	switch len(mine) {
	case 0:
		return here.Session{}, false, ""
	case 1:
		return mine[0], true, ""
	}
	return here.Session{}, false, fmt.Sprintf("Moving this session into %s here: its agent's process runs %d sessions, and nothing says which one ran this command, "+
		"so none was moved. A session that starts in this repo is in %[1]s from the start.", brand.Product, len(mine))
}

// repoOf is the working tree a git common dir belongs to, for saying which repo.
func repoOf(gitDir string) string {
	if filepath.Base(gitDir) == ".git" {
		return filepath.Dir(gitDir)
	}
	return gitDir
}
