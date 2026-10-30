//go:build !windows

package msgsock

import (
	"context"
	"net"
)

func dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}
