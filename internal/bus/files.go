package bus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/a-Fig/murmur/internal/brand"
)

// File is one file a message carries (#570, docs/design/files.md). The file itself isn't in the
// event: it is a blob, encrypted with Key, in the commit on the message's files ref (FilesRef),
// which a device fetches only when the message is for it. The event is encrypted for the team, so
// whoever can read the message can read its files, and nobody else.
type File struct {
	Name string `json:"name"`
	Size int64  `json:"size"` // of the file, not of its blob
	Sum  string `json:"sum"`  // SHA-256 of the file, in hex
	Key  string `json:"key"`  // the AES-256 key its blob is encrypted with, in base64
}

// What a message's files may come to. Every recipient's device fetches them, and the remote keeps
// them until the sender deletes the ref.
const (
	MaxFiles      = 8
	MaxFile       = 10 << 20
	MaxFilesTotal = 20 << 20
	// maxFileName is the longest name FileName gives, in runes: under the 255 bytes a file system
	// allows a name, whatever the letters.
	maxFileName = 60
	// fileOverhead is how much longer a file's blob is than the file: the nonce and GCM's tag.
	fileOverhead = 12 + 16
)

// FilesRef is the ref that holds the files of the message with that ID, sent from that device: one
// commit, whose tree names each file's blob by its place in the event's list ("0", "1", ...).
func FilesRef(device, msgID string) string { return brand.FilesPrefix + device + "/" + msgID }

// FileName makes of what a sender called a file a name a receiver can save it under, on any
// system, inside the directory Murmur chose: letters, digits, '.', '_' and '-' only, so never a
// path, a hidden file, or a name Windows keeps for a device. What a name loses is replaced by '_'.
func FileName(s string) string {
	if i := strings.LastIndexAny(s, `/\`); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '.' || r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	name := strings.TrimLeft(b.String(), ".")
	if r := []rune(name); len(r) > maxFileName {
		// Cut the stem, so "a-long-name.png" stays a .png.
		ext := ""
		if i := strings.LastIndex(name, "."); i > 0 && len(name)-i <= 12 {
			ext = name[i:]
		}
		stem := []rune(strings.TrimSuffix(name, ext))
		name = string(stem[:maxFileName-len([]rune(ext))]) + ext
	}
	name = strings.TrimRight(name, ".") // Windows drops a trailing dot, and two names become one
	if name == "" {
		return "file"
	}
	stem, _, _ := strings.Cut(name, ".")
	switch up := strings.ToUpper(stem); {
	case up == "CON", up == "PRN", up == "AUX", up == "NUL",
		len(up) == 4 && (strings.HasPrefix(up, "COM") || strings.HasPrefix(up, "LPT")) && up[3] >= '0' && up[3] <= '9':
		return "_" + name
	}
	return name
}

// validFile says f is what Murmur writes of a file: a size within the limit, a SHA-256 and a key.
func validFile(f File) bool {
	if f.Size < 0 || f.Size > MaxFile || len(f.Sum) != 2*sha256.Size {
		return false
	}
	if _, err := hex.DecodeString(f.Sum); err != nil {
		return false
	}
	key, err := b64.DecodeString(f.Key)
	return err == nil && len(key) == 32
}

// cleanFiles holds a message's files to what Murmur writes. A file that isn't keeps its place, with
// nothing but a name: its blob is named by that place, and a reader shows it as one that couldn't be
// read. Names are made safe and distinct, since they become file names on the receiver's disk. Files
// past MaxFiles are dropped, and any other event carries none.
func cleanFiles(typ string, in []File) []File {
	if typ != Message {
		return nil
	}
	return CleanFiles(in)
}

// CleanFiles is cleanFiles for a message's files: a sender runs it too, so the names it writes are
// the names every receiver saves the files under.
func CleanFiles(in []File) []File {
	if len(in) == 0 {
		return nil
	}
	if len(in) > MaxFiles {
		in = in[:MaxFiles]
	}
	out := make([]File, 0, len(in))
	seen := map[string]bool{}
	for _, f := range in {
		if !validFile(f) {
			f = File{Name: f.Name}
		}
		name := FileName(f.Name)
		for n := 2; seen[strings.ToLower(name)]; n++ {
			name = strconv.Itoa(n) + "-" + FileName(f.Name)
		}
		seen[strings.ToLower(name)] = true
		f.Name = name
		out = append(out, f)
	}
	return out
}

// Readable says the file's blob can be opened: cleanFiles left it its size, sum and key.
func (f File) Readable() bool { return f.Key != "" }

func fileAAD(msgID string, index int) []byte {
	return []byte("file\x00" + msgID + "\x00" + strconv.Itoa(index))
}

// SealFile encrypts a file for the message with that ID, as the index'th of its files, and returns
// the blob to store and the size, sum and key the event says of it. The blob is bound to the
// message and the place: moved to another, it doesn't open.
func SealFile(plain []byte, msgID string, index int) (blob []byte, f File, err error) {
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	if _, err := rand.Read(key); err != nil {
		return nil, File{}, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, File{}, err
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, File{}, err
	}
	sum := sha256.Sum256(plain)
	blob = aead.Seal(nonce, nonce, plain, fileAAD(msgID, index))
	return blob, File{Size: int64(len(plain)), Sum: hex.EncodeToString(sum[:]), Key: b64.EncodeToString(key)}, nil
}

// errBadFile is a file's blob that isn't the file its event describes.
var errBadFile = errors.New("a file that doesn't open")

// OpenFile decrypts the blob of the index'th file of the message with that ID, and holds it to
// the size and sum the event gives.
func OpenFile(blob []byte, f File, msgID string, index int) ([]byte, error) {
	key, err := b64.DecodeString(f.Key)
	if err != nil || len(key) != 32 || len(blob) < fileOverhead {
		return nil, errBadFile
	}
	aead, err := gcm(key)
	if err != nil {
		return nil, errBadFile
	}
	plain, err := aead.Open(nil, blob[:12], blob[12:], fileAAD(msgID, index))
	if err != nil {
		return nil, errBadFile
	}
	sum := sha256.Sum256(plain)
	want, _ := hex.DecodeString(f.Sum)
	if int64(len(plain)) != f.Size || subtle.ConstantTimeCompare(sum[:], want) != 1 {
		return nil, errBadFile
	}
	return plain, nil
}

// NewFilesCommit writes blobs as one commit with no parent, whose tree names each by its place
// ("0", "1", ...), and returns the commit. Like NewCommit it writes objects and nothing else, with
// plumbing that never reads the index or the working tree.
func (r *Repo) NewFilesCommit(ctx context.Context, blobs [][]byte) (string, error) {
	var tree strings.Builder
	for i, blob := range blobs {
		out, err := r.Git.Run(ctx, string(blob), "hash-object", "-t", "blob", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&tree, "100644 blob %s\t%d\n", strings.TrimSpace(out), i)
	}
	out, err := r.Git.Run(ctx, tree.String(), "mktree")
	if err != nil {
		return "", err
	}
	out, err = r.Git.Run(ctx, "files\n", "-c", "user.name="+r.Name, "-c", "user.email="+r.Email,
		"commit-tree", "--no-gpg-sign", strings.TrimSpace(out), "-F", "-")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ErrNoFile is a file's blob that isn't in the commit on its message's files ref.
var ErrNoFile = errors.New("not on the message's files ref")

// ReadFileBlob returns the index'th blob of the files commit at rev (a SHA or a ref), which was
// fetched. One over max bytes isn't read: git stores a blob compressed, so a small fetch can hold
// one of any size (see MaxRecord).
func (r *Repo) ReadFileBlob(ctx context.Context, rev string, index int, max int64) ([]byte, error) {
	name := rev + ":" + strconv.Itoa(index)
	out, err := r.Git.Run(ctx, name+"\n", "cat-file", "--batch-check=%(objecttype) %(objectsize)")
	if err != nil {
		return nil, err
	}
	f := strings.Fields(out)
	if len(f) != 2 || f[0] != "blob" {
		return nil, ErrNoFile
	}
	size, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return nil, ErrNoFile
	}
	if size > max {
		return nil, fmt.Errorf("%w: a file of %d bytes, over %d", ErrTooLarge, size, max)
	}
	out, err = r.Git.Run(ctx, "", "cat-file", "blob", name)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// FetchFiles fetches the commit a files ref holds on the remote and returns it. Nothing local
// names it after: the objects are read once and left for git's own housekeeping. ErrNoFile if the
// ref isn't on the remote. The fetch asks for the ref by name, as not every server gives an object
// asked for by its ID; a ref that moves between the listing and the fetch reads as files that
// aren't there.
func (r *Repo) FetchFiles(ctx context.Context, ref string) (string, error) {
	sha, err := r.RemoteRef(ctx, ref)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", ErrNoFile
	}
	_, err = r.Git.Run(ctx, "", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "fetch", "--quiet",
		"--no-tags", "--no-write-fetch-head", "--no-recurse-submodules",
		"--negotiation-tip="+brand.RefRoot+"*", r.Remote, ref)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// MaxFileBlob is the largest blob a file within MaxFile makes.
const MaxFileBlob = MaxFile + fileOverhead

// DeleteLocal removes a local ref, if it is there.
func (r *Repo) DeleteLocal(ctx context.Context, ref string) error {
	_, err := r.Git.Run(ctx, "", "update-ref", "-d", ref)
	return err
}
