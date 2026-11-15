package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-Fig/murmur/internal/brand"
	"github.com/a-Fig/murmur/internal/bus"
	"github.com/a-Fig/murmur/internal/proto"
	"github.com/a-Fig/murmur/internal/scrub"
)

// Files a message carries (#570, docs/design/files.md). The sender's daemon encrypts each and
// pushes them as one blob to a ref of the message's own (bus.FilesRef) before it writes the event. A
// receiving daemon fetches that ref only for a message that is for one of its sessions, saves the
// files under its state directory, and queues the message once they are there, so the paths the
// agent is given work by the time it reads them.

const (
	// fileFetchLimit bounds one try at fetching a message's files; fileTries is how many tries a
	// message gets before it is queued without them.
	fileFetchLimit = time.Minute
	fileTries      = 3
)

// fileWait is a message held back from the inboxes of sessions here until its files are fetched.
type fileWait struct {
	Event bus.Event `json:"event"`
	For   []string  `json:"for"` // the sessions it is for
}

// filesDir is where the files of the message with that ID are saved: inside the git directory,
// never the working tree.
func (d *Daemon) filesDir(msgID string) string { return filepath.Join(d.dir, "files", msgID) }

// readSendFiles reads the files a send names and holds them to the limits. A text file is checked
// for secrets as a body is; anything else can't be.
func readSendFiles(paths []string) ([][]byte, []bus.File, error) {
	if len(paths) > bus.MaxFiles {
		return nil, nil, fmt.Errorf("a message carries at most %d files, and this has %d", bus.MaxFiles, len(paths))
	}
	var total int64
	data := make([][]byte, 0, len(paths))
	files := make([]bus.File, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, nil, fmt.Errorf("%s isn't a full path", path)
		}
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return nil, nil, fmt.Errorf("can't read %s: %v", path, errors.Unwrap(err))
		case !info.Mode().IsRegular():
			return nil, nil, fmt.Errorf("%s isn't a file", path)
		case info.Size() > bus.MaxFile:
			return nil, nil, fmt.Errorf("%s is %s, and a file can be at most %d MB", path, proto.Size(info.Size()), bus.MaxFile>>20)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, fmt.Errorf("can't read %s: %v", path, errors.Unwrap(err))
		}
		b, err := io.ReadAll(io.LimitReader(f, bus.MaxFile+1)) // it may have grown since the look
		f.Close()
		switch {
		case err != nil:
			return nil, nil, fmt.Errorf("can't read %s: %v", path, err)
		case len(b) > bus.MaxFile:
			return nil, nil, fmt.Errorf("%s is over %d MB, the most a file can be", path, bus.MaxFile>>20)
		}
		if total += int64(len(b)); total > bus.MaxFilesTotal {
			return nil, nil, fmt.Errorf("a message's files can come to at most %d MB together", bus.MaxFilesTotal>>20)
		}
		if utf8.Valid(b) && !slices.Contains(b, 0) {
			if err := scrub.Check(string(b)); err != nil {
				return nil, nil, fmt.Errorf("%s: %v", path, err)
			}
		}
		data = append(data, b)
		files = append(files, bus.File{Name: filepath.Base(path)})
	}
	return data, files, nil
}

// putFiles encrypts the files a send names and pushes them to a new files ref, for the message
// with that ID, and returns what the event says of them and the ref's name. It keeps a copy of each
// where a receiver's would be, so a session on this device, and the page, have them without a fetch.
func (d *Daemon) putFiles(ctx context.Context, msgID string, paths []string) (files []bus.File, at string, err error) {
	data, files, err := readSendFiles(paths)
	if err != nil {
		return nil, "", err
	}
	sealed := make([][]byte, len(data))
	for i, b := range data {
		blob, f, err := bus.SealFile(b, msgID, i)
		if err != nil {
			return nil, "", err
		}
		f.Name = files[i].Name
		sealed[i], files[i] = blob, f
	}
	files = bus.CleanFiles(files) // the names a receiver will save them under
	pack, err := bus.PackFiles(sealed)
	if err != nil {
		return nil, "", err
	}
	sha, err := d.repo.NewFilesCommit(ctx, pack)
	if err != nil {
		return nil, "", err
	}
	at = bus.NewFilesAt()
	d.mu.Lock()
	d.st.FileRefs[at] = time.Now().Round(0)
	d.saveLocked()
	d.mu.Unlock()
	if err := d.push(ctx, sha, bus.FilesRef(at), bus.FastForward, ""); err != nil {
		d.dropFileRef(at, false)
		return nil, "", fmt.Errorf("its files didn't go out: %w", err)
	}
	for i, f := range files {
		if err := d.saveFile(msgID, f.Name, data[i]); err != nil {
			d.log.Printf("keeping a copy of %s, sent with message %s: %v", f.Name, msgID, err)
		}
	}
	return files, at, nil
}

// dropFileRef forgets a files ref this device pushed, or with due set leaves it to the next prune
// to delete from the remote: its message never went out.
func (d *Daemon) dropFileRef(at string, due bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if due {
		d.st.FileRefs[at] = time.Now().Add(-d.opt.FileKeep).Round(0)
	} else {
		delete(d.st.FileRefs, at)
	}
	d.saveLocked()
}

// saveFile writes one file of a message where its sessions are told it is. It is written beside
// its place and renamed, so a path that exists holds the whole file.
func (d *Daemon) saveFile(msgID, name string, data []byte) error {
	dir := d.filesDir(msgID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// filesHere says every file of e that can be read is saved here.
func (d *Daemon) filesHere(e bus.Event) bool {
	for _, f := range e.Files {
		if !f.Readable() {
			continue
		}
		if _, err := os.Stat(filepath.Join(d.filesDir(e.ID), f.Name)); err != nil {
			return false
		}
	}
	return true
}

// holdForFilesLocked says message e, for session id, waits for its files: they aren't here yet, so
// the fetch is started and the message is queued when it ends, with or without them. A person's
// page asks for a file when it shows one, so nothing is held for them. Caller holds mu.
func (d *Daemon) holdForFilesLocked(id string, e bus.Event) bool {
	if len(e.Files) == 0 || id == bus.PersonSession || d.filesHere(e) {
		return false
	}
	w := d.st.FilesWait[e.ID]
	if w == nil {
		w = &fileWait{Event: e}
		d.st.FilesWait[e.ID] = w
	}
	if !slices.Contains(w.For, id) {
		w.For = append(w.For, id)
	}
	d.kickFiles()
	return true
}

func (d *Daemon) kickFiles() {
	select {
	case d.fileKick <- struct{}{}:
	default:
	}
}

// fileLoop fetches the files of the messages that wait for them, one message at a time, and
// deletes what is past FileKeep.
func (d *Daemon) fileLoop(ctx context.Context) {
	prune := time.NewTicker(d.opt.FilePrune)
	defer prune.Stop()
	d.pruneFiles(ctx)
	for {
		for {
			d.mu.Lock()
			ids := sortedKeys(d.st.FilesWait)
			var e bus.Event
			if len(ids) > 0 {
				e = d.st.FilesWait[ids[0]].Event
			}
			d.mu.Unlock()
			if len(ids) == 0 || ctx.Err() != nil {
				break
			}
			err := d.fetchFiles(ctx, e)
			if ctx.Err() != nil {
				return // still waiting when the daemon next starts
			}
			if err != nil {
				d.log.Printf("message %s is queued without its files: %v", e.ID, err)
			}
			d.mu.Lock()
			if w := d.st.FilesWait[e.ID]; w != nil {
				delete(d.st.FilesWait, e.ID)
				for _, id := range w.For {
					d.queueInboxLocked(id, w.Event)
				}
				d.saveLocked()
			}
			d.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-d.fileKick:
		case <-prune.C:
			d.pruneFiles(ctx)
		}
	}
}

// fetchFiles brings the files of message e from its files ref and saves them. A fetch that fails
// is tried again, as the network may be why; a ref that isn't there, or holds something else, is
// not.
func (d *Daemon) fetchFiles(ctx context.Context, e bus.Event) error {
	if d.filesHere(e) {
		return nil
	}
	if e.FilesAt == "" {
		return bus.ErrNoFile
	}
	ref := bus.FilesRef(e.FilesAt)
	var sha string
	var err error
	for try := 1; ; try++ {
		fctx, cancel := context.WithTimeout(ctx, fileFetchLimit)
		sha, err = d.repo.FetchFiles(fctx, ref)
		cancel()
		if err == nil || errors.Is(err, bus.ErrNoFile) || try == fileTries || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(try) * 5 * time.Second):
		}
	}
	if err != nil {
		return err
	}
	pack, err := d.repo.ReadPack(ctx, sha, bus.MaxPack)
	if err != nil {
		return err
	}
	var failed []string
	for i, f := range e.Files {
		if !f.Readable() {
			continue
		}
		blob, err := bus.PackedFile(pack, e.Files, i)
		var plain []byte
		if err == nil {
			plain, err = bus.OpenFile(blob, f, e.ID, i)
		}
		if err == nil {
			err = d.saveFile(e.ID, f.Name, plain)
		}
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", f.Name, err))
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

// pruneFiles deletes the copies of files kept longer than FileKeep, and from the remote the files
// refs of messages this device sent that long ago.
func (d *Daemon) pruneFiles(ctx context.Context) {
	root := filepath.Join(d.dir, "files")
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > d.opt.FileKeep {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				d.log.Printf("deleting the files of message %s: %v", entry.Name(), err)
			}
		}
	}
	d.mu.Lock()
	var due []string
	for at, sent := range d.st.FileRefs {
		if time.Since(sent) > d.opt.FileKeep {
			due = append(due, at)
		}
	}
	d.mu.Unlock()
	slices.Sort(due)
	for _, at := range due {
		// Deleting a ref that is already gone succeeds: the look after a refused push finds it at
		// "", which is what was asked for.
		if err := d.push(ctx, "", bus.FilesRef(at), bus.Force, ""); err != nil {
			d.log.Printf("deleting files ref %s from the remote: %v", at, err)
			if ctx.Err() != nil {
				return
			}
			continue
		}
		d.dropFileRef(at, false)
	}
}

// noFilesNoteLocked tells the sender of message e, which carries files, which of its recipients
// are on a device whose daemon is from before files: they get the text alone, with no sign that
// there was more. Caller holds mu.
func (d *Daemon) noFilesNoteLocked(e bus.Event) string {
	to, _ := d.recipientsOfLocked(e)
	var old []string
	for _, addr := range to {
		dev, _ := bus.SplitAddr(addr)
		if p := d.peers[dev]; dev != d.device && p != nil && !p.locked && !p.pres.Files {
			name, _, _ := d.sessionInfoLocked(addr)
			old = append(old, nameOr(name, addr))
		}
	}
	switch len(old) {
	case 0:
		return ""
	case 1:
		return old[0] + " is on a device that runs a " + brand.Product + " from before files, so it gets the text alone."
	}
	return strings.Join(old[:len(old)-1], ", ") + " and " + old[len(old)-1] + " are on devices that run a " +
		brand.Product + " from before files, so they get the text alone."
}

// fileViewsLocked is the files of message e as a session here is told of them: each with the path
// it is saved at, or none if it isn't here (it couldn't be fetched, or is past FileKeep). Caller
// holds mu.
func (d *Daemon) fileViewsLocked(e bus.Event) []proto.FileView {
	if len(e.Files) == 0 {
		return nil
	}
	views := make([]proto.FileView, 0, len(e.Files))
	for _, f := range e.Files {
		v := proto.FileView{Name: f.Name, Size: f.Size}
		if path := filepath.Join(d.filesDir(e.ID), f.Name); f.Readable() {
			if _, err := os.Stat(path); err == nil {
				v.Path = path
			}
		}
		views = append(views, v)
	}
	return views
}
