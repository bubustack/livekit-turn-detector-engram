package engram

import (
	"context"
	"log/slog"

	sdk "github.com/bubustack/bubu-sdk-go"
)

func (e *LiveKitTurnDetector) debugEnabled(ctx context.Context, logger *slog.Logger) bool {
	if sdk.DebugModeEnabled() {
		return true
	}
	if logger == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return logger.Enabled(ctx, slog.LevelDebug)
}

func (e *LiveKitTurnDetector) logDetectorInput(ctx context.Context, logger *slog.Logger, pkt *audioPacket) {
	if !e.debugEnabled(ctx, logger) || pkt == nil {
		return
	}
	logger.Debug("turn-detector received audio",
		slog.String("participant", pkt.Participant.Identity),
		slog.String("room", pkt.Room.Name),
		slog.Int("bytes", len(pkt.Audio.Data)),
		slog.Int("sampleRate", pkt.Audio.SampleRate),
		slog.Int("channels", pkt.Audio.Channels),
		slog.String("encoding", pkt.Audio.Encoding),
	)
}

func (e *LiveKitTurnDetector) logDetectorOutput(ctx context.Context, logger *slog.Logger, pkt *turnPacket) {
	if !e.debugEnabled(ctx, logger) || pkt == nil {
		return
	}
	payloadBytes := 0
	if pkt.Audio.Data != nil {
		payloadBytes = len(pkt.Audio.Data)
	}
	logger.Debug("turn-detector emitted VAD chunk",
		slog.String("participant", pkt.Participant.Identity),
		slog.Int("sequence", pkt.Sequence),
		slog.Int("durationMs", pkt.DurationMs),
		slog.Int("bytes", payloadBytes),
		slog.String("encoding", pkt.Audio.Encoding),
	)
}

func (e *LiveKitTurnDetector) logIgnoredPacket(
	ctx context.Context,
	logger *slog.Logger,
	pkt *audioPacket,
	reason string,
) {
	if !e.debugEnabled(ctx, logger) || pkt == nil {
		return
	}
	logger.Debug("turn-detector ignored packet",
		slog.String("participant", pkt.Participant.Identity),
		slog.String("room", pkt.Room.Name),
		slog.String("reason", reason),
		slog.String("type", pkt.Type),
	)
}

func (e *LiveKitTurnDetector) logChunkBatch(ctx context.Context, logger *slog.Logger, participant string, count int) {
	if !e.debugEnabled(ctx, logger) || count == 0 {
		return
	}
	logger.Debug("turn-detector detected speech batch",
		slog.String("participant", participant),
		slog.Int("chunks", count),
	)
}
