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
	"github.com/idreamshen/anylan/internal/logmem"
)

func main() {
	logs := logmem.InstallDefault(logmem.DefaultLimit)
	runControl(logs)
}

func runControl(logs *logmem.Recorder) {
	fs := flag.NewFlagSet("anylan-client", flag.ExitOnError)
	web := fs.String("web", client.DefaultWebAddr, "HTTP WebUI listen address")
	webToken := fs.String("web-token", "", "bearer token required for the HTTP WebUI")
	_ = fs.Parse(os.Args[1:])
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: anylan-client [--web 127.0.0.1:18081] [--web-token token]")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := client.RunControl(ctx, *web, *webToken, logs); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
