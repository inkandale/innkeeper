// Command fakeplugin is a plugin for core tests. The -mode flag makes it
// misbehave in specific ways.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

func main() {
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | bad-errors | silent | crash-unknown | no-shutdown")
	flag.Parse()

	var conn *protocol.Conn
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			res := protocol.InitializeResult{
				Protocol:     protocol.Version,
				Name:         "fake",
				Version:      "0.0.1",
				Capabilities: []string{"push", "pull"},
			}
			switch *mode {
			case "bad-protocol":
				res.Protocol = 99
			case "wrong-caps":
				res.Capabilities = []string{"push"}
			case "wrong-version":
				res.Version = "9.9.9"
			}
			return res, nil
		case protocol.MethodShutdown:
			if *mode == "no-shutdown" {
				time.Sleep(time.Minute)
			}
			return nil, nil
		case "echo":
			return params, nil
		case "shout":
			fmt.Fprintf(os.Stderr, "%s\nafter\n", strings.Repeat("x", 100_000))
			return nil, nil
		case "env":
			var name string
			_ = json.Unmarshal(params, &name)
			return os.Getenv(name), nil
		case "callback":
			var out string
			err := conn.Call(ctx, "core.ping", nil, &out)
			return out, err
		case "fail":
			return nil, protocol.NewError(-32000, "temporary failure", true)
		case "sleep":
			var ms int
			if err := json.Unmarshal(params, &ms); err != nil {
				return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return "slept", nil
		case "crash":
			os.Exit(3)
		case "freeze":
			// A notification handler blocks the reader, so the plugin
			// stops reading stdin, like a hung plugin.
			time.Sleep(time.Minute)
		}
		switch *mode {
		case "bad-errors":
			return nil, nil
		case "silent":
			time.Sleep(time.Minute)
			return nil, nil
		case "crash-unknown":
			os.Exit(4)
		}
		return nil, protocol.MethodNotFound(method)
	}

	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *mode == "no-shutdown" {
		time.Sleep(time.Minute)
	}
}
