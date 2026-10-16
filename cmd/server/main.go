package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bkucharz/falsepoker/internal/room"
	"github.com/bkucharz/falsepoker/internal/transport"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address")
	origin := flag.String("origin", "http://localhost:8080", "exact public application origin")
	devHTTP := flag.Bool("dev-http", false, "allow non-Secure cookies for explicit loopback HTTP development")
	maxRooms := flag.Int("max-rooms", room.DefaultMaxRooms, "maximum live rooms")
	maxMembers := flag.Int("max-members", room.DefaultMaxMembers, "maximum members per room (at least 24)")
	flag.Parse()
	if *devHTTP {
		host, _, err := net.SplitHostPort(*addr)
		ip := net.ParseIP(host)
		if err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return errors.New("-dev-http requires a loopback listen address")
		}
	}
	registry, err := room.NewRegistryWithLimits(*maxRooms, *maxMembers)
	if err != nil {
		return err
	}
	handler, err := transport.NewServer(transport.Config{Origin: *origin, AllowInsecureCookies: *devHTTP, Registry: registry})
	if err != nil {
		return err
	}
	defer handler.Close()
	server := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Printf("HTTP server listening on %s; application origin %s; WebSockets are not implemented yet", listener.Addr(), *origin)
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	select {
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		handler.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			if closeErr := server.Close(); closeErr != nil {
				return errors.Join(err, closeErr)
			}
			return err
		}
		return nil
	}
}
