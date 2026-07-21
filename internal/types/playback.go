package types

type PlaybackAvailability string

const (
	PlaybackAvailable   PlaybackAvailability = "available"
	PlaybackMissing     PlaybackAvailability = "missing"
	PlaybackCorrupt     PlaybackAvailability = "corrupt"
	PlaybackUnavailable PlaybackAvailability = "unavailable"
)

type PlaybackPayload struct {
	Digest           string               `json:"digest"`
	Availability     PlaybackAvailability `json:"availability"`
	MediaType        string               `json:"media_type,omitempty"`
	Size             int64                `json:"size"`
	OriginalSize     int64                `json:"original_size,omitempty"`
	Reason           string               `json:"reason,omitempty"`
	References       []string             `json:"references"`
	Preview          string               `json:"preview,omitempty"`
	PreviewEncoding  string               `json:"preview_encoding,omitempty"`
	PreviewTruncated bool                 `json:"preview_truncated,omitempty"`
}

type PlaybackWarning struct {
	Code       string   `json:"code"`
	Digest     string   `json:"digest"`
	References []string `json:"references"`
	Message    string   `json:"message"`
}

type PlaybackResult struct {
	SchemaVersion               string              `json:"schema_version"`
	Mode                        string              `json:"mode"`
	Executed                    bool                `json:"executed"`
	ApplicationProcessesStarted int                 `json:"application_processes_started"`
	OutboundRequests            int                 `json:"outbound_requests"`
	ManifestVerified            bool                `json:"manifest_verified"`
	Capsule                     RunCapsule          `json:"capsule"`
	Completeness                CapsuleCompleteness `json:"completeness"`
	Payloads                    []PlaybackPayload   `json:"payloads"`
	Warnings                    []PlaybackWarning   `json:"warnings"`
}
