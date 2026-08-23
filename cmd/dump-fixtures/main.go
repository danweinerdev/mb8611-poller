// Command dump-fixtures captures raw HNAP responses from an MB8611 for tests.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danweinerdev/mb8611-poller/internal/hnap"
)

var actions = []string{
	"GetMotoStatusDownstreamChannelInfo",
	"GetMotoStatusUpstreamChannelInfo",
	"GetMotoStatusConnectionInfo",
	"GetMotoStatusSoftware",
	"GetMotoStatusStartupSequence",
	"GetMotoStatusLog",
}

func main() {
	destination := flag.String("dir", "internal/hnap/hnaptest/testdata", "fixture destination directory")
	flag.Parse()
	host, username, password := os.Getenv("MB8611_MODEM_HOST"), os.Getenv("MB8611_MODEM_USERNAME"), os.Getenv("MB8611_MODEM_PASSWORD")
	if host == "" || username == "" || password == "" {
		fmt.Fprintln(os.Stderr, "MB8611_MODEM_HOST, MB8611_MODEM_USERNAME, and MB8611_MODEM_PASSWORD are required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := hnap.New(hnap.Config{Host: host, Username: username, Password: password, InsecureTLS: true})
	if err := client.EnsureLogin(ctx); err != nil {
		fail(err)
	}
	defer client.Logout(context.Background())
	responses, err := client.Multi(ctx, actions...)
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(*destination, 0o755); err != nil {
		fail(err)
	}
	for _, action := range actions {
		response, ok := responses[action+"Response"]
		if !ok {
			fail(fmt.Errorf("missing %s response", action))
		}
		if err := os.WriteFile(filepath.Join(*destination, action+".json"), response, 0o600); err != nil {
			fail(err)
		}
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, "dump fixtures:", err); os.Exit(1) }
