package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/client"
	"github.com/a-Fig/murmur/internal/join"
)

// runUI opens the page for humans: the daemon serves it, and starts if it isn't running. The
// address carries the page's token in its fragment, so whoever has the address has the page: what
// it shows, this device's settings and session tags. It is not the token sessions use, which lets
// its holder speak for any of this device's sessions, so a leak through the browser's history or
// an extension doesn't give that (#42). The browser is handed a file that leads to the address,
// not the address itself (see writeOpener), so open is given the file's path.
func runUI(ctx context.Context, args []string, out io.Writer, open func(path string) error) error {
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
	address, err := c.PageURL()
	if errors.Is(err, client.ErrNoPage) {
		return fmt.Errorf("the daemon isn't serving the page. If it was started by an older version, "+
			"stop it with `%s stop` and run this again; if not, the reason is in %s",
			brand.Command, filepath.Join(client.StateDir(gitDir), "daemon.log"))
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, address)
	if *noOpen {
		return nil
	}
	path, err := writeOpener(client.StateDir(gitDir), address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "couldn't write the file that opens the page (%v); open the address above\n", err)
		return nil
	}
	if err := open(path); err != nil {
		fmt.Fprintf(os.Stderr, "couldn't open a browser (%v); open the address above\n", err)
	}
	return nil
}

// openerName is the file, in the daemon's state directory, that `mur ui` hands the browser.
const openerName = "open-page.html"

// opener is that file: a page that goes on at once to the page's address, with a link for a
// browser that runs no script and follows no refresh. html/template escapes the address for each
// place it goes. The token is in the address's fragment, which no browser sends on; no-referrer
// is for any that would.
var opener = template.Must(template.New(openerName).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0; url={{.URL}}">
<title>Opening {{.Product}}</title>
<script>location.replace({{.URL}});</script>
</head>
<body>
<p>Opening {{.Product}}. If nothing happens, <a href="{{.URL}}">open the page</a>.</p>
</body>
</html>
`))

// writeOpener writes the file that leads the browser to address, and returns its path. The address
// carries the page's token, and the arguments of the program that hands it to the browser can be
// read by other users of the machine (on Linux, by any of them), so the browser is given this
// file's path instead (#220; Jupyter does the same). Only its owner can read the file, and it is
// in the state directory, which only its owner can open and which already holds the token in
// daemon.json; where proc.Protect leaves that directory as it is (a network drive, a volume
// without access lists, on Windows), the file is as private as daemon.json, no more. Each `mur ui` replaces it by a rename, so no wider permissions, or half a page, are
// left from before. It isn't deleted: the token in it stops working with the daemon's page, and
// while the page runs daemon.json holds the same token beside it.
func writeOpener(dir, address string) (string, error) {
	var b bytes.Buffer
	if err := opener.Execute(&b, struct{ Product, URL string }{brand.Product, address}); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, openerName+".*") // mode 0600
	if err != nil {
		return "", err
	}
	_, err = f.Write(b.Bytes())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	path := filepath.Join(dir, openerName)
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return path, nil
}

// browse opens the file at path in the default browser. It doesn't wait for the browser to exit,
// only to see that the program that hands the file to it has not failed.
func browse(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 hands FileProtocolHandler the rest of its command line unparsed, quotes and
		// all, so it is given a file URL, which has no spaces to need them.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", fileURL(path))
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
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

// fileURL is the file URL of an absolute path: file:///C:/Users/A%20B/x.html for
// C:\Users\A B\x.html, and file://server/share/A%20B/x.html for \\server\share\A B\x.html. A UNC
// path's server is the URL's host: Windows reads file:////server/... as a path that keeps its %20.
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if rest, ok := strings.CutPrefix(p, "//"); ok {
		host, share, _ := strings.Cut(rest, "/")
		return (&url.URL{Scheme: "file", Host: host, Path: "/" + share}).String()
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
