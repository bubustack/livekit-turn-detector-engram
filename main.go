package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/bubustack/bubu-sdk-go"
	vadengram "github.com/bubustack/livekit-turn-detector-engram/pkg/engram"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := sdk.StartStreaming(ctx, vadengram.New()); err != nil {
		log.Fatalf("livekit-turn-detector engram failed: %v", err)
	}
}
