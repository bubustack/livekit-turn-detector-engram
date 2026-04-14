package engram

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdkengram "github.com/bubustack/bubu-sdk-go/engram"
	"github.com/bubustack/tractatus/transport"
)

func structuredAudioPacket(roomName string) []byte {
	return []byte(`{
		"type":"speech.audio.v1",
		"room":{"name":"` + roomName + `"},
		"participant":{"identity":"speaker"},
		"audio":{"encoding":"` + audioCodecPCM + `","sampleRate":16000,"channels":1,"data":"AQI="}
	}`)
}

func TestEmitTurnSignal(t *testing.T) {
	ctx := context.Background()
	var capturedKey string
	var capturedPayload map[string]any
	orig := emitSignalFunc
	emitSignalFunc = func(_ context.Context, key string, payload any) error {
		capturedKey = key
		if m, ok := payload.(map[string]any); ok {
			capturedPayload = m
		}
		return nil
	}
	t.Cleanup(func() { emitSignalFunc = orig })

	s := &streamState{}
	room := roomInfo{Name: "demo", SID: "room-sid"}
	participant := participantInfo{Identity: "speaker", SID: "speaker-sid"}
	audio := audioBuffer{Encoding: audioCodecPCM, SampleRate: 48000, Channels: 1}

	s.emitTurnSignal(ctx, s.logger, room, participant, audio, 7, 320)

	if capturedKey != "speech.turn.v1" {
		t.Fatalf("expected speech.turn.v1, got %s", capturedKey)
	}
	if capturedPayload["participant"] != "speaker" {
		t.Fatalf("expected participant=speaker, got %v", capturedPayload["participant"])
	}
	if capturedPayload["room"] != "demo" {
		t.Fatalf("expected room=demo, got %v", capturedPayload["room"])
	}
	if capturedPayload["sequence"] != 7 {
		t.Fatalf("expected sequence=7, got %v", capturedPayload["sequence"])
	}
	if capturedPayload["durationMs"] != 320 {
		t.Fatalf("expected durationMs=320, got %v", capturedPayload["durationMs"])
	}
	if capturedPayload["encoding"] != audioCodecPCM {
		t.Fatalf("expected encoding pcm, got %v", capturedPayload["encoding"])
	}
}

func TestSendTurnEmitsValidStructuredStreamMessage(t *testing.T) {
	ctx := context.Background()
	out := make(chan sdkengram.StreamMessage, 1)
	s := &streamState{}

	room := roomInfo{Name: " demo-room ", SID: "RM1"}
	participant := participantInfo{Identity: " speaker-1 ", SID: "PS1"}
	pcm := make([]byte, 320)

	if err := s.sendTurn(ctx, nil, out, room, participant, pcm, 16000, 1, 7, audioCodecPCM); err != nil {
		t.Fatalf("sendTurn returned error: %v", err)
	}

	select {
	case msg := <-out:
		assertValidTurnMessage(t, msg)
	default:
		t.Fatal("expected one message emitted")
	}
}

func TestPacketFromStreamMessagePrefersInputsOverPayloadAndBinary(t *testing.T) {
	msg := sdkengram.NewInboundMessage(sdkengram.StreamMessage{
		Inputs:  structuredAudioPacket("inputs-room"),
		Payload: structuredAudioPacket("payload-room"),
		Binary:  &sdkengram.BinaryFrame{Payload: structuredAudioPacket("binary-room")},
	})

	packet, ok, err := packetFromStreamMessage(msg)
	if err != nil {
		t.Fatalf("packetFromStreamMessage returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected packet")
	}
	if packet.Room.Name != "inputs-room" {
		t.Fatalf("expected inputs-backed packet, got room %q", packet.Room.Name)
	}
}

func TestPacketFromStreamMessageDefaultsStructuredAudioFields(t *testing.T) {
	msg := sdkengram.NewInboundMessage(sdkengram.StreamMessage{
		Payload: []byte(`{"type":"speech.audio.v1","participant":{"identity":"speaker"},"audio":{"data":"AQI="}}`),
	})

	packet, ok, err := packetFromStreamMessage(msg)
	if err != nil {
		t.Fatalf("packetFromStreamMessage returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected packet")
	}
	if packet.Audio.SampleRate != 48000 {
		t.Fatalf("expected default sample rate 48000, got %d", packet.Audio.SampleRate)
	}
	if packet.Audio.Channels != 1 {
		t.Fatalf("expected default channels 1, got %d", packet.Audio.Channels)
	}
	if packet.Audio.Encoding != audioCodecPCM {
		t.Fatalf("expected default encoding pcm, got %q", packet.Audio.Encoding)
	}
}

func assertValidTurnMessage(t *testing.T, msg sdkengram.StreamMessage) {
	t.Helper()
	if err := msg.Validate(); err != nil {
		t.Fatalf("emitted message should be valid, got %v", err)
	}
	if msg.Audio != nil {
		t.Fatalf("expected binary-only message, got audio frame")
	}
	if msg.Binary == nil {
		t.Fatalf("expected binary frame")
	}
	if string(msg.Payload) != string(msg.Binary.Payload) {
		t.Fatalf("expected mirrored payload, payload=%q binary=%q", string(msg.Payload), string(msg.Binary.Payload))
	}
	if msg.MessageID != "speaker-1-7" {
		t.Fatalf("unexpected message ID: %q", msg.MessageID)
	}
	if msg.Metadata["participant"] != "speaker-1" {
		t.Fatalf("unexpected participant metadata: %q", msg.Metadata["participant"])
	}
	if msg.Metadata["room.name"] != "demo-room" {
		t.Fatalf("unexpected room.name metadata: %q", msg.Metadata["room.name"])
	}
	var packet turnPacket
	if err := json.Unmarshal(msg.Payload, &packet); err != nil {
		t.Fatalf("failed to decode binary packet: %v", err)
	}
	if packet.Type != transport.StreamTypeSpeechVADActive {
		t.Fatalf("unexpected packet type: %q", packet.Type)
	}
	if packet.Sequence != 7 {
		t.Fatalf("unexpected packet sequence: %d", packet.Sequence)
	}
	if packet.DurationMs <= 0 {
		t.Fatalf("expected positive duration, got %d", packet.DurationMs)
	}
	if packet.Timestamp.IsZero() || packet.Timestamp.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("unexpected packet timestamp: %v", packet.Timestamp)
	}
}
