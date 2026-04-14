# 🎙️ LiveKit Turn Detector Engram

This engram buffers the short PCM chunks emitted by the `livekit-bridge-engram` ingress,
runs WebRTC VAD to detect speech activity, and forwards utterance-sized audio
turns (by default as WAV files) downstream. It is intended to sit between the
ingress and STT steps so that transcription services receive properly framed
audio instead of hundreds of 10 ms packets.

## 🌟 Highlights

- Uses the pure-Go WebRTC VAD port (no cgo).
- Configurable frame size, aggressiveness, silence tail, and min/max turn
  durations.
- Emits `speech.vad.active` payloads (the shared Tractatus stream type for VAD turns)
  that contain a sequence number, duration, and the buffered audio (WAV or PCM).

## 🚀 Quick Start

```bash
make lint
go test ./...
make docker-build
```

Apply `Engram.yaml` and place the turn detector between your ingress audio step
and downstream STT/VAD consumers.

## ⚙️ Configuration (`Engram.spec.with`)

| Name               | Default | Description |
|--------------------|---------|-------------|
| `frameDurationMs`  | `20`    | Frame size fed into the VAD (10/20/30 ms supported). |
| `silenceDurationMs`| `400`   | Required trailing silence before a turn is closed. |
| `minDurationMs`    | `200`   | Minimum speech duration for a valid turn. |
| `maxDurationMs`    | `10000` | Hard cap for a single buffered turn. |
| `aggressiveness`   | `2`     | WebRTC VAD mode (0 = relaxed, 3 = very aggressive). |
| `outputEncoding`   | `wav`   | `wav` wraps the PCM chunk in a WAVE header; `pcm` keeps raw PCM16. |

## 📥 Inputs

Input messages must be the `speech.audio.v1` packets emitted by the
ingress engram (PCM16 mono, typically 48 kHz).

## 📤 Outputs

Output messages have `type=speech.vad.active` and contain the buffered audio
inline plus metadata about the participant, room, duration, and a
monotonically increasing sequence number per speaker.

## 🔄 Streaming Mode

| Stream type | Description |
|-------------|-------------|
| `speech.vad.active` | Emitted whenever WebRTC VAD detects a contiguous speech turn. Includes buffered audio. |

Downstream steps can simply filter on the `speech.vad.*` namespace regardless of which
detector produced the signal, enabling mix-and-match pipelines.

## 🧪 Local Development

- `make lint` – Run the shared lint and static-analysis checks.
- `go test ./...` – Run the turn-detector test suite.
- `make docker-build` – Build the engram image for local clusters.
- Toggle `BUBU_DEBUG=true` to log sanitized packet summaries and turn boundaries without dumping raw PCM into the logs.

## 🤝 Community & Support

- [Contributing](./CONTRIBUTING.md)
- [Support](./SUPPORT.md)
- [Security Policy](./SECURITY.md)
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Discord](https://discord.gg/dysrB7D8H6)


## 📄 License

Copyright 2025 BubuStack.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
