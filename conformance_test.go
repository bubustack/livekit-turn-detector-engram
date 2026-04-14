package main

import (
	"testing"

	"github.com/bubustack/bubu-sdk-go/conformance"
	"github.com/bubustack/livekit-turn-detector-engram/pkg/config"
	"github.com/bubustack/livekit-turn-detector-engram/pkg/engram"
)

func TestStreamConformance(t *testing.T) {
	suite := conformance.StreamSuite[config.Config]{
		Engram: engram.New(),
		Config: config.Config{},
	}
	suite.Run(t)
}
