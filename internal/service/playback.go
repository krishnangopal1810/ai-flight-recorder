package service

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const playbackPreviewBytes = 64 * 1024

func (s *CapsuleService) Playback(ctx context.Context, id string) (types.PlaybackResult, error) {
	capsule, err := s.repository.GetCapsule(ctx, id)
	if err != nil {
		return types.PlaybackResult{}, err
	}
	if err := VerifyCapsuleManifest(capsule); err != nil {
		return types.PlaybackResult{}, err
	}
	references := capturedPayloadReferences(capsule)
	result := types.PlaybackResult{
		SchemaVersion:    types.SchemaVersion,
		Mode:             "playback",
		ManifestVerified: true,
		Capsule:          capsule,
		Payloads:         []types.PlaybackPayload{},
		Warnings:         []types.PlaybackWarning{},
	}
	for _, digest := range capsule.Integrity.PayloadDigests {
		payload := playbackPayloadMetadata(digest, references[digest])
		data, loadErr := s.repository.LoadPayload(ctx, digest)
		if loadErr == nil {
			payload.Availability = types.PlaybackAvailable
			payload.Size = int64(len(data))
			payload.Preview, payload.PreviewEncoding, payload.PreviewTruncated = playbackPreview(data, payload.MediaType)
			result.Payloads = append(result.Payloads, payload)
			continue
		}
		payload.Availability = playbackAvailability(loadErr)
		result.Payloads = append(result.Payloads, payload)
		result.Warnings = append(result.Warnings, types.PlaybackWarning{
			Code:       "payload_" + string(payload.Availability),
			Digest:     digest,
			References: append([]string(nil), payload.References...),
			Message:    playbackWarningMessage(payload.Availability),
		})
	}
	result.Completeness = playbackCompleteness(capsule, result.Warnings)
	return result, nil
}

func playbackCompleteness(capsule types.RunCapsule, warnings []types.PlaybackWarning) types.CapsuleCompleteness {
	report := CompletenessReport(capsule)
	for _, warning := range warnings {
		item := types.MissingItem{Code: warning.Code, Message: warning.Message}
		report.Playback.Missing = append(report.Playback.Missing, item)
		if logOnlyReferences(warning.References) {
			continue
		}
		report.Diff.Missing = append(report.Diff.Missing, item)
		report.Replay.Missing = append(report.Replay.Missing, item)
		report.Bisect.Missing = append(report.Bisect.Missing, item)
	}
	report.Playback = failClosedForIssues(report.Playback, false)
	report.Diff = failClosedForIssues(report.Diff, false)
	report.Replay = failClosedForIssues(report.Replay, true)
	report.Bisect = failClosedForIssues(report.Bisect, true)
	return report
}

func logOnlyReferences(references []string) bool {
	if len(references) == 0 {
		return false
	}
	for _, reference := range references {
		if !strings.HasPrefix(reference, "logs[") {
			return false
		}
	}
	return true
}

func playbackPayloadMetadata(digest string, references []payloadReference) types.PlaybackPayload {
	payload := types.PlaybackPayload{Digest: digest, References: []string{}}
	for _, reference := range references {
		payload.References = append(payload.References, reference.path)
		if payload.MediaType == "" {
			payload.MediaType = reference.ref.MediaType
		}
		if payload.Size == 0 {
			payload.Size = reference.ref.Size
		}
		if payload.OriginalSize == 0 {
			payload.OriginalSize = reference.ref.OriginalSize
		}
		if payload.Reason == "" {
			payload.Reason = reference.ref.Reason
		}
	}
	sort.Strings(payload.References)
	return payload
}

func playbackAvailability(err error) types.PlaybackAvailability {
	if errors.Is(err, repository.ErrPayloadCorrupt) {
		return types.PlaybackCorrupt
	}
	var flightErr *types.FlightError
	if errors.As(err, &flightErr) && flightErr.Code == types.ErrNotFound {
		return types.PlaybackMissing
	}
	return types.PlaybackUnavailable
}

func playbackWarningMessage(availability types.PlaybackAvailability) string {
	switch availability {
	case types.PlaybackMissing:
		return "recorded payload bytes are missing; unaffected capsule evidence remains inspectable"
	case types.PlaybackCorrupt:
		return "recorded payload bytes failed digest verification; the corrupt value is not rendered"
	default:
		return "recorded payload bytes could not be read; unaffected capsule evidence remains inspectable"
	}
}

func playbackPreview(data []byte, mediaType string) (string, string, bool) {
	end := min(len(data), playbackPreviewBytes)
	truncated := end < len(data)
	if utf8.Valid(data) {
		for end > 0 && !utf8.Valid(data[:end]) {
			end--
		}
		encoding := "utf8"
		if isJSONMediaType(mediaType) {
			encoding = "json"
		}
		return string(data[:end]), encoding, truncated
	}
	return base64.StdEncoding.EncodeToString(data[:end]), "base64", truncated
}
