package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ptxt-nstr/internal/apprun"
	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/memlimit"
)

func main() {
	memlimit.ApplyDefaultFromEnv()
	instance, err := apprun.Start(context.Background(), config.Load())
	if err != nil {
		log.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-serverDone(instance):
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := instance.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}

func serverDone(instance *apprun.Instance) <-chan error {
	done := make(chan error, 1)
	go func() { done <- instance.Wait() }()
	return done
}
