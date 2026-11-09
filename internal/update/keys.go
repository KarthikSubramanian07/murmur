package update

// releaseKeys are the public halves of the keys release.yml signs releases with (#193), one
// `ssh-ed25519 AAAA...` line each, as `go run ./internal/update/signrelease -generate` prints them.
// While the list is empty, an update is checked against its release's SHA256SUMS only. Once it has
// a key, this binary installs only a release signed by one of them, and release.yml fails a release
// it can't sign with one. docs/releasing.md says how to add one and how to rotate.
var releaseKeys = []string{}
