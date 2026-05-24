package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/idreamshen/anylan/internal/client"
	"github.com/idreamshen/anylan/internal/tap"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "join" {
		fmt.Fprintln(os.Stderr, "usage: anylan-client join --server host:4433 --room room-code [--dev tap-device]")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("join", flag.ExitOnError)
	server := fs.String("server", "", "anylan-server address")
	room := fs.String("room", "", "room code")
	name := fs.String("name", "", "optional display name")
	dev := fs.String("dev", tap.DefaultDeviceName(), "TAP device name")
	insecureSkipVerify := fs.Bool("insecure-skip-verify", true, "skip server certificate verification for local development")
	web := fs.String("web", "0.0.0.0:8081", "HTTP status listen address")
	_ = fs.Parse(os.Args[2:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := client.Config{
		Server:             *server,
		Room:               *room,
		DisplayName:        *name,
		DeviceName:         *dev,
		InsecureSkipVerify: *insecureSkipVerify,
		WebAddr:            *web,
	}
	if err := client.Run(ctx, cfg); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
