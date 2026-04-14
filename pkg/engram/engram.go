package engram

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/bubustack/bubu-sdk-go"
	sdkengram "github.com/bubustack/bubu-sdk-go/engram"
	cfgpkg "github.com/bubustack/livekit-turn-detector-engram/pkg/config"
	"github.com/bubustack/tractatus/transport"
	"github.com/bytectlgo/webrtcvad-go"
)

const (
	bytesPerSample   = 2
	audioPayloadType = transport.StreamTypeSpeechAudio
	audioCodecPCM    = "pcm"
	audioCodecPCM16  = "pcm16"
)

var emitSignalFunc = sdk.EmitSignal

type LiveKitTurnDetector struct {
	cfg cfgpkg.Config
}

func New() *LiveKitTurnDetector {
	return &LiveKitTurnDetector{}
}

func (e *LiveKitTurnDetector) Init(_ context.Context, cfg cfgpkg.Config, _ *sdkengram.Secrets) error {
	e.cfg = cfgpkg.Normalize(cfg)
	return nil
}

func (e *LiveKitTurnDetector) Stream(
	ctx context.Context,
	in <-chan sdkengram.InboundMessage,
	out chan<- sdkengram.StreamMessage,
) error {
	logger := sdk.LoggerFromContext(ctx).With("component", "livekit-turn-detector")
	logger.Info("ENGRAM: Stream method entered, initializing state")
	state := newStreamState(e, e.cfg, logger)
	logger.Info("ENGRAM: State initialized, entering processing loop")

	for {
		select {
		case <-ctx.Done():
			return state.flushAll(ctx, out)
		case msg, ok := <-in:
			if !ok {
				return state.flushAll(ctx, out)
			}

			pkt, found, err := packetFromStreamMessage(msg)
			if err != nil {
				logger.Error("failed to parse audio packet", "error", err)
				msg.Done()
				continue
			}
			if !found {
				if isHeartbeat(msg.Metadata) {
					msg.Done()
					continue
				}
				logger.Warn("ignoring message without audio", "hasAudio", msg.Audio != nil, "metadata", msg.Metadata)
				msg.Done()
				continue
			}
			if !isSupportedAudioPacket(pkt.Type) {
				e.logIgnoredPacket(ctx, logger, &pkt, "unsupported_audio_type")
				logger.Warn("ignoring unsupported audio packet type", "type", pkt.Type)
				msg.Done()
				continue
			}
			if pkt.Participant.Identity == "" {
				e.logIgnoredPacket(ctx, logger, &pkt, "missing_participant_identity")
				logger.Warn("ignoring audio without participant identity", "metadata", msg.Metadata)
				msg.Done()
				continue
			}
			e.logDetectorInput(ctx, logger, &pkt)

			chunks, err := state.ingest(pkt)
			if err != nil {
				logger.Error("failed to process audio", "error", err)
				msg.Done()
				continue
			}
			e.logChunkBatch(ctx, logger, pkt.Participant.Identity, len(chunks))
			for _, chunk := range chunks {
				seq := state.nextSequence(pkt.Participant.Identity)
				durationMs := calcDurationMs(len(chunk), pkt.Audio.SampleRate, pkt.Audio.Channels)
				logger.Info("emitting buffered turn",
					"participant", pkt.Participant.Identity,
					"sequence", seq,
					"bytes", len(chunk),
					"durationMs", durationMs,
				)
				if err := state.sendTurn(
					ctx,
					logger,
					out,
					pkt.Room,
					pkt.Participant,
					chunk,
					pkt.Audio.SampleRate,
					pkt.Audio.Channels,
					seq,
					e.cfg.OutputEncoding,
				); err != nil {
					return err
				}
			}
			msg.Done()
		}
	}
}

func packetFromStreamMessage(msg sdkengram.InboundMessage) (audioPacket, bool, error) {
	if msg.Audio != nil && len(msg.Audio.PCM) > 0 {
		sampleRate := int(msg.Audio.SampleRateHz)
		if sampleRate == 0 {
			sampleRate = 48000
		}
		channels := int(msg.Audio.Channels)
		if channels == 0 {
			channels = 1
		}
		return audioPacket{
			Type: audioPayloadType,
			Room: roomInfo{
				Name: strings.TrimSpace(msg.Metadata["room.name"]),
				SID:  strings.TrimSpace(msg.Metadata["room.sid"]),
			},
			Participant: participantInfo{
				Identity: strings.TrimSpace(msg.Metadata["participant.id"]),
				SID:      strings.TrimSpace(msg.Metadata["participant.sid"]),
			},
			Audio: audioBuffer{
				Encoding:   strings.ToLower(msg.Audio.Codec),
				SampleRate: sampleRate,
				Channels:   channels,
				Data:       msg.Audio.PCM,
			},
		}, true, nil
	}

	raw := streamStructuredBytes(msg)
	if len(raw) == 0 {
		return audioPacket{}, false, nil
	}
	var pkt audioPacket
	if err := json.Unmarshal(raw, &pkt); err != nil {
		return audioPacket{}, false, err
	}
	if pkt.Type == "" {
		pkt.Type = audioPayloadType
	}
	if pkt.Audio.SampleRate <= 0 {
		pkt.Audio.SampleRate = 48000
	}
	if pkt.Audio.Channels <= 0 {
		pkt.Audio.Channels = 1
	}
	if strings.TrimSpace(pkt.Audio.Encoding) == "" {
		pkt.Audio.Encoding = "pcm"
	}
	return pkt, true, nil
}

func streamStructuredBytes(msg sdkengram.InboundMessage) []byte {
	if len(msg.Inputs) > 0 {
		return msg.Inputs
	}
	if len(msg.Payload) > 0 {
		return msg.Payload
	}
	if msg.Binary != nil && len(msg.Binary.Payload) > 0 {
		return msg.Binary.Payload
	}
	return nil
}

func isSupportedAudioPacket(packetType string) bool {
	packetType = strings.TrimSpace(packetType)
	return packetType == "" || strings.EqualFold(packetType, audioPayloadType)
}

type streamState struct {
	owner        *LiveKitTurnDetector
	cfg          cfgpkg.Config
	logger       *slog.Logger
	buffers      map[string]*turnBuffer
	contexts     map[string]participantContext
	seqBySpeaker map[string]int
	mu           sync.Mutex
}

func newStreamState(owner *LiveKitTurnDetector, cfg cfgpkg.Config, logger *slog.Logger) *streamState {
	return &streamState{
		owner:        owner,
		cfg:          cfg,
		logger:       logger,
		buffers:      make(map[string]*turnBuffer),
		contexts:     make(map[string]participantContext),
		seqBySpeaker: make(map[string]int),
	}
}

func (s *streamState) buffer(key string, sampleRate, channels int) (*turnBuffer, error) {
	if buf, ok := s.buffers[key]; ok {
		return buf, nil
	}
	v, err := webrtcvad.New(s.cfg.Aggressiveness)
	if err != nil {
		return nil, err
	}
	buf, err := newTurnBuffer(v, s.cfg, sampleRate, channels)
	if err != nil {
		return nil, err
	}
	s.buffers[key] = buf
	return buf, nil
}

func (s *streamState) ingest(pkt audioPacket) ([][]byte, error) {
	s.contexts[pkt.Participant.Identity] = participantContext{
		room:        pkt.Room,
		participant: pkt.Participant,
	}
	buf, err := s.buffer(pkt.Participant.Identity, pkt.Audio.SampleRate, pkt.Audio.Channels)
	if err != nil {
		return nil, err
	}
	return buf.ingest(pkt.Audio.Data)
}

func (s *streamState) flushAll(ctx context.Context, out chan<- sdkengram.StreamMessage) error {
	for speaker, buf := range s.buffers {
		ctxInfo := s.contexts[speaker]
		chunks := buf.flushForce()
		for _, chunk := range chunks {
			seq := s.nextSequence(speaker)
			room := ctxInfo.room
			participant := ctxInfo.participant
			if participant.Identity == "" {
				participant.Identity = speaker
			}
			if s.logger != nil {
				durationMs := calcDurationMs(len(chunk), buf.sampleRate, buf.channels)
				s.logger.Info("emitting buffered turn during flush",
					"participant", participant.Identity,
					"sequence", seq,
					"bytes", len(chunk),
					"durationMs", durationMs,
				)
			}
			if err := s.sendTurn(
				ctx,
				s.logger,
				out,
				room,
				participant,
				chunk,
				buf.sampleRate,
				buf.channels,
				seq,
				s.cfg.OutputEncoding,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *streamState) nextSequence(participant string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqBySpeaker[participant]++
	return s.seqBySpeaker[participant]
}

type turnBuffer struct {
	vad               *webrtcvad.VAD
	sampleRate        int
	channels          int
	frameDurationMs   int
	frameBytes        int
	frameSamples      int
	silenceSamplesReq int
	minSamples        int
	maxSamples        int

	rawData            []byte
	analysisBuf        []byte
	bytesConsumed      int
	processedSamples   int
	samplesSinceSpeech int
	inSpeech           bool
	hadSpeech          bool
}

func newTurnBuffer(vad *webrtcvad.VAD, cfg cfgpkg.Config, sampleRate, channels int) (*turnBuffer, error) {
	frameBytes := frameSizeBytes(cfg.FrameDurationMs, sampleRate, channels)
	if frameBytes <= 0 {
		return nil, fmt.Errorf("unsupported frame duration %dms for sample rate %d", cfg.FrameDurationMs, sampleRate)
	}
	frameSamples := frameBytes / (channels * bytesPerSample)
	silenceFrames := max(1, cfg.SilenceDuration/cfg.FrameDurationMs)
	minFrames := max(1, cfg.MinDurationMs/cfg.FrameDurationMs)
	maxFrames := cfg.MaxDurationMs / cfg.FrameDurationMs

	return &turnBuffer{
		vad:               vad,
		sampleRate:        sampleRate,
		channels:          channels,
		frameDurationMs:   cfg.FrameDurationMs,
		frameBytes:        frameBytes,
		frameSamples:      frameSamples,
		silenceSamplesReq: silenceFrames * frameSamples,
		minSamples:        minFrames * frameSamples,
		maxSamples:        maxFrames * frameSamples,
	}, nil
}

func (t *turnBuffer) ingest(data []byte) ([][]byte, error) {
	t.rawData = append(t.rawData, data...)
	t.analysisBuf = append(t.analysisBuf, data...)
	var outputs [][]byte

	for len(t.analysisBuf) >= t.frameBytes {
		frame := make([]byte, t.frameBytes)
		copy(frame, t.analysisBuf[:t.frameBytes])
		t.analysisBuf = t.analysisBuf[t.frameBytes:]
		t.bytesConsumed += t.frameBytes
		t.processedSamples += t.frameSamples

		isSpeech, err := t.vad.IsSpeech(frame, t.sampleRate)
		if err != nil {
			return outputs, err
		}
		if isSpeech {
			t.inSpeech = true
			t.hadSpeech = true
			t.samplesSinceSpeech = 0
		} else if t.inSpeech {
			t.samplesSinceSpeech += t.frameSamples
		}

		shouldFlush := false
		if t.hadSpeech && t.samplesSinceSpeech >= t.silenceSamplesReq && t.processedSamples >= t.minSamples {
			shouldFlush = true
		}
		if t.hadSpeech && t.maxSamples > 0 && t.processedSamples >= t.maxSamples {
			shouldFlush = true
		}
		if shouldFlush {
			if chunk := t.popChunk(false); len(chunk) > 0 {
				outputs = append(outputs, chunk)
			}
		}
	}
	return outputs, nil
}

func (t *turnBuffer) flushForce() [][]byte {
	if len(t.rawData) == 0 || !t.hadSpeech {
		t.resetState()
		return nil
	}
	chunk := t.popChunk(true)
	if len(chunk) == 0 {
		return nil
	}
	return [][]byte{chunk}
}

func (t *turnBuffer) popChunk(includePartial bool) []byte {
	var chunkLen int
	if includePartial {
		chunkLen = len(t.rawData)
	} else {
		chunkLen = t.bytesConsumed
	}
	if chunkLen <= 0 {
		t.resetState()
		return nil
	}
	chunk := make([]byte, chunkLen)
	copy(chunk, t.rawData[:chunkLen])
	if includePartial {
		t.resetState()
	} else {
		t.rawData = t.rawData[chunkLen:]
		t.bytesConsumed = 0
		t.processedSamples = 0
		t.samplesSinceSpeech = 0
		t.inSpeech = false
		t.hadSpeech = false
	}
	return chunk
}

func (t *turnBuffer) resetState() {
	t.rawData = nil
	t.analysisBuf = nil
	t.bytesConsumed = 0
	t.processedSamples = 0
	t.samplesSinceSpeech = 0
	t.inSpeech = false
	t.hadSpeech = false
}

type roomInfo struct {
	Name string `json:"name"`
	SID  string `json:"sid"`
}

type participantInfo struct {
	Identity string `json:"identity"`
	SID      string `json:"sid"`
}

type participantContext struct {
	room        roomInfo
	participant participantInfo
}

type audioBuffer struct {
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sampleRate"`
	Channels   int    `json:"channels"`
	Data       []byte `json:"data"`
}

type audioPacket struct {
	Type        string          `json:"type"`
	Room        roomInfo        `json:"room"`
	Participant participantInfo `json:"participant"`
	Audio       audioBuffer     `json:"audio"`
}

type turnPacket struct {
	Type        string          `json:"type"`
	Room        roomInfo        `json:"room"`
	Participant participantInfo `json:"participant"`
	Sequence    int             `json:"sequence"`
	DurationMs  int             `json:"durationMs"`
	Timestamp   time.Time       `json:"timestamp"`
	Audio       audioBuffer     `json:"audio"`
}

func (s *streamState) sendTurn(
	ctx context.Context,
	logger *slog.Logger,
	out chan<- sdkengram.StreamMessage,
	room roomInfo,
	participant participantInfo,
	pcm []byte,
	sampleRate, channels, sequence int,
	encoding string,
) error {
	roomName := strings.TrimSpace(room.Name)
	roomSID := strings.TrimSpace(room.SID)
	participantID := strings.TrimSpace(participant.Identity)
	participantSID := strings.TrimSpace(participant.SID)
	if participantID == "" {
		participantID = participantSID
	}
	if participantID == "" {
		participantID = "unknown"
	}
	payloadAudio := audioBuffer{
		Encoding:   strings.ToLower(encoding),
		SampleRate: sampleRate,
		Channels:   channels,
		Data:       pcm,
	}
	if payloadAudio.Encoding == "wav" {
		payloadAudio.Data = wrapPCMAsWAV(pcm, sampleRate, channels)
	}
	durationMs := calcDurationMs(len(pcm), sampleRate, channels)
	packet := turnPacket{
		Type:        transport.StreamTypeSpeechVADActive,
		Room:        room,
		Participant: participant,
		Sequence:    sequence,
		DurationMs:  durationMs,
		Timestamp:   time.Now().UTC(),
		Audio:       payloadAudio,
	}
	if s != nil && s.owner != nil {
		s.owner.logDetectorOutput(ctx, logger, &packet)
	}
	body, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	audioPayload := append([]byte(nil), pcm...)
	audioCodec := canonicalAudioCodec(payloadAudio.Encoding)
	message := sdkengram.StreamMessage{
		Kind:      transport.StreamTypeSpeechVADActive,
		MessageID: fmt.Sprintf("%s-%d", participantID, sequence),
		Payload:   body,
		Metadata: map[string]string{
			"source":      "livekit",
			"provider":    "livekit-turn-detector",
			"type":        transport.StreamTypeSpeechVADActive,
			"participant": participantID,
			"room.name":   roomName,
			"room.sid":    roomSID,
			"sequence":    strconv.Itoa(sequence),
		},
		Binary: &sdkengram.BinaryFrame{
			MimeType: "application/json",
			Payload:  body,
		},
	}
	// Keep compatibility hints for downstream consumers that parse packet metadata.
	message.Metadata["audio.codec"] = audioCodec
	message.Metadata["audio.sampleRate"] = strconv.Itoa(sampleRate)
	message.Metadata["audio.channels"] = strconv.Itoa(channels)
	message.Metadata["audio.bytes"] = strconv.Itoa(len(audioPayload))
	select {
	case out <- message:
		s.emitTurnSignal(ctx, logger, room, participant, payloadAudio, sequence, durationMs)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *streamState) emitTurnSignal(
	ctx context.Context,
	logger *slog.Logger,
	room roomInfo,
	participant participantInfo,
	audio audioBuffer,
	sequence, durationMs int,
) {
	payload := map[string]any{
		"type":       "speech.turn.v1",
		"sequence":   sequence,
		"durationMs": durationMs,
	}
	if participant.Identity != "" {
		payload["participant"] = participant.Identity
	}
	if participant.SID != "" {
		payload["participantSid"] = participant.SID
	}
	if room.Name != "" {
		payload["room"] = room.Name
	}
	if audio.SampleRate > 0 {
		payload["sampleRate"] = audio.SampleRate
	}
	if audio.Channels > 0 {
		payload["channels"] = audio.Channels
	}
	if audio.Encoding != "" {
		payload["encoding"] = audio.Encoding
	}
	if logger == nil {
		logger = sdk.LoggerFromContext(ctx)
	}
	if err := emitSignalFunc(ctx, "speech.turn.v1", payload); err != nil && !errors.Is(err, sdk.ErrSignalsUnavailable) {
		logger.Warn("Failed to emit speech turn signal", "error", err)
	}
}

func wrapPCMAsWAV(data []byte, sampleRate, channels int) []byte {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if channels <= 0 {
		channels = 1
	}
	blockAlign := channels * bytesPerSample
	byteRate := sampleRate * blockAlign
	chunkSize := 36 + len(data)

	buf := bytes.NewBuffer(make([]byte, 0, chunkSize+8))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(chunkSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes()
}

func frameSizeBytes(frameDurationMs, sampleRate, channels int) int {
	if frameDurationMs <= 0 || sampleRate <= 0 || channels <= 0 {
		return 0
	}
	samples := sampleRate * frameDurationMs / 1000
	return samples * channels * bytesPerSample
}

func calcDurationMs(bytesLen, sampleRate, channels int) int {
	if sampleRate <= 0 || channels <= 0 {
		return 0
	}
	samples := bytesLen / (channels * bytesPerSample)
	if samples == 0 {
		return 0
	}
	return samples * 1000 / sampleRate
}

func isHeartbeat(meta map[string]string) bool {
	if meta == nil {
		return false
	}
	return meta["bubu-heartbeat"] == "true"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func canonicalAudioCodec(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", audioCodecPCM, audioCodecPCM16, "pcm-16", "pcm16le":
		return audioCodecPCM16
	case "wav", "wave", "audio/wav":
		return audioCodecPCM16
	default:
		return normalized
	}
}
