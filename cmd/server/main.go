package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/idreamshen/anylan/internal/logmem"
	"github.com/idreamshen/anylan/internal/server"
)

func main() {
	logs := logmem.InstallDefault(logmem.DefaultLimit)
	var (
		listen          = flag.String("listen", ":4433", "UDP listen address")
		pool            = flag.String("pool", "10.240.0.0/12", "IPv4 pool for room /24 allocations")
		tlsCert         = flag.String("tls-cert", "", "TLS certificate path")
		tlsKey          = flag.String("tls-key", "", "TLS private key path")
		insecureDevCert = flag.Bool("insecure-dev-cert", false, "generate an ephemeral self-signed certificate for local development")
		mtu             = flag.Int("mtu", 1300, "TAP MTU announced to clients")
		web             = flag.String("web", "", "HTTP status listen address")
		webToken        = flag.String("web-token", "", "bearer token required for the HTTP status page")
	)
	flag.Parse()

	parsedPool, err := server.LoadPool(*pool)
	if err != nil {
		log.Fatalf("invalid pool: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.Server{
		Addr:            *listen,
		Pool:            parsedPool,
		TLSCertFile:     *tlsCert,
		TLSKeyFile:      *tlsKey,
		InsecureDevCert: *insecureDevCert,
		MTU:             *mtu,
		WebAddr:         *web,
		WebToken:        *webToken,
		Logs:            logs,
	}
	log.Printf("anylan-server listening on %s with pool %s", *listen, parsedPool)
	if err := srv.ListenAndServe(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
