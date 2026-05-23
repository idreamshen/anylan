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
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "join" {
		fmt.Fprintln(os.Stderr, "usage: anylan-client join --server host:4433 --room room-code [--dev anylan0]")
		os.Exit(2)
	}

	fs := flag.NewFlagSet("join", flag.ExitOnError)
	server := fs.String("server", "", "anylan-server address")
	room := fs.String("room", "", "room code")
	name := fs.String("name", "", "optional display name")
	dev := fs.String("dev", "anylan0", "TAP device name")
	insecureSkipVerify := fs.Bool("insecure-skip-verify", false, "skip server certificate verification for local development")
	_ = fs.Parse(os.Args[2:])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := client.Config{
		Server:             *server,
		Room:               *room,
		DisplayName:        *name,
		DeviceName:         *dev,
		InsecureSkipVerify: *insecureSkipVerify,
	}
	if err := client.Run(ctx, cfg); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
