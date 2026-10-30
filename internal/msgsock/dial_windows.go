package msgsock

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// pipePrefix starts the name of every named pipe. Claude Code on Windows offers its socket as one;
// anything else is taken for a unix socket, which Windows has too.
const pipePrefix = `\\.\pipe\`

func dial(ctx context.Context, path string) (net.Conn, error) {
	if strings.HasPrefix(path, pipePrefix) {
		return winio.DialPipeContext(ctx, path)
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
