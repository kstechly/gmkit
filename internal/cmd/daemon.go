package cmd

import (
	"context"
	"github.com/johnlindquist/gmkit/internal/rpc"
)

// callDaemon uses the configured session owner. A missing socket must never
// create another phone connection using the caller's credential environment.
func callDaemon(ctx context.Context, method string, params, result any) error {
	layout, err := resolveLayout()
	if err != nil {
		return err
	}
	client, err := rpc.Dial(layout.Socket)
	if err != nil {
		return err
	}
	defer client.Close()
	return client.Call(ctx, method, params, result)
}
