package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "0.1.0"

func main() {
	path := flag.String("config", "/etc/node-guard/config.json", "configuration file")
	once := flag.Bool("once", false, "perform one cycle and print status")
	observe := flag.Bool("observe", false, "probe but do not PUT group selections (native groups may still react)")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("node-guard " + version)
		return
	}
	c, err := loadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	secret, err := readSecret(c.SecretFile)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	g := newGuard(c, newClient(c, secret), *observe)
	log.Printf("node-guard %s interval=%dms timeout=%dms observe=%t", version, c.IntervalMS, c.TimeoutMS, *observe)
	if *once {
		g.cycle(ctx)
		_ = json.NewEncoder(os.Stdout).Encode(g.status)
		if g.status.LastError != "" {
			os.Exit(1)
		}
		return
	}
	ticker := time.NewTicker(time.Duration(c.IntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		g.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
