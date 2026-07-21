package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

func New(baseURL, token string) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, types.NewError(types.ErrValidation, "invalid Flight service URL", "use http://host:port or https://host:port", err)
	}
	return &Client{base: parsed, token: token, http: &http.Client{Timeout: 75 * time.Second}}, nil
}

func (c *Client) ListCapsules(ctx context.Context, project string, limit int) ([]types.CapsuleSummary, error) {
	query := url.Values{}
	if project != "" {
		query.Set("project", project)
	}
	if limit > 0 {
		query.Set("limit", fmt.Sprint(limit))
	}
	var response struct {
		Items []types.CapsuleSummary `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/capsules?"+query.Encode(), nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *Client) GetCapsule(ctx context.Context, id string) (types.RunCapsule, error) {
	var result types.RunCapsule
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capsules/"+url.PathEscape(id), nil, &result)
	return result, err
}
func (c *Client) Completeness(ctx context.Context, id string) (types.CapsuleCompleteness, error) {
	var result types.CapsuleCompleteness
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capsules/"+url.PathEscape(id)+"/completeness", nil, &result)
	return result, err
}
func (c *Client) Playback(ctx context.Context, id string) (map[string]any, error) {
	var result map[string]any
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capsules/"+url.PathEscape(id)+"/playback", nil, &result)
	return result, err
}
func (c *Client) Diff(ctx context.Context, baseline, candidate string) (types.DiffResult, error) {
	var result types.DiffResult
	body := map[string]string{"baseline_capsule_id": baseline, "candidate_capsule_id": candidate}
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/diffs", body, &result)
	return result, err
}

func (c *Client) Export(ctx context.Context, id, filename string) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.resolve("/api/v1/capsules/"+url.PathEscape(id)+"/export"), nil)
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot contact Flight service", "start flight serve or check --url", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return decodeError(response)
	}
	temp, err := os.CreateTemp(path.Dir(filename), ".flight-export-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := io.Copy(temp, response.Body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filename)
}

func (c *Client) Import(ctx context.Context, filename string) (types.RunCapsule, error) {
	file, err := os.Open(filename)
	if err != nil {
		return types.RunCapsule{}, err
	}
	defer file.Close()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/api/v1/capsules/import"), file)
	request.Header.Set("Content-Type", "application/zstd")
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return types.RunCapsule{}, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return types.RunCapsule{}, decodeError(response)
	}
	var capsule types.RunCapsule
	err = json.NewDecoder(response.Body).Decode(&capsule)
	return capsule, err
}

func (c *Client) CreateToken(ctx context.Context, name string, scopes []string) (map[string]any, error) {
	var result map[string]any
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/tokens", map[string]any{"name": name, "scopes": scopes}, &result)
	return result, err
}

func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/tokens/"+url.PathEscape(id), nil, nil)
}

func (c *Client) CreateReplay(ctx context.Context, request types.CreateReplayRequest) (types.Job, error) {
	var result types.Job
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/replays", request, &result)
	return result, err
}

func (c *Client) GetJob(ctx context.Context, id string) (types.Job, error) {
	var result types.Job
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/jobs/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) IngestOTLP(ctx context.Context, export *collectortrace.ExportTraceServiceRequest) ([]types.RunCapsule, error) {
	data, err := proto.Marshal(export)
	if err != nil {
		return nil, err
	}
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.resolve("/v1/traces"), bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/x-protobuf")
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot contact Flight OTLP endpoint", "start flight serve or check --url", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, decodeError(response)
	}
	var accepted collectortrace.ExportTraceServiceResponse
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(body, &accepted); err != nil {
		return nil, err
	}
	if accepted.PartialSuccess != nil && accepted.PartialSuccess.RejectedSpans > 0 {
		return nil, types.NewError(types.ErrValidation, "demo OTLP spans were rejected", accepted.PartialSuccess.ErrorMessage, nil)
	}
	// The collector identity is deterministic; fetch the newest demo capsules.
	summaries, err := c.ListCapsules(ctx, "demo", 10)
	if err != nil {
		return nil, err
	}
	result := []types.RunCapsule{}
	for _, summary := range summaries {
		capsule, getErr := c.GetCapsule(ctx, summary.CapsuleID)
		if getErr != nil {
			return nil, getErr
		}
		result = append(result, capsule)
	}
	return result, nil
}

func (c *Client) ValidateEvaluator(ctx context.Context, definition types.EvaluatorDefinition) error {
	var result struct {
		Valid bool `json:"valid"`
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/evaluators/validate", definition, &result)
}

func (c *Client) RunEvaluator(ctx context.Context, request types.EvaluatorRunRequest) (types.EvaluatorRun, error) {
	var result types.EvaluatorRun
	err := c.doJSON(ctx, http.MethodPost, "/api/v1/evaluators/run", request, &result)
	return result, err
}

func (c *Client) GetEvaluator(ctx context.Context, id, version string) (types.EvaluatorDefinition, error) {
	query := url.Values{}
	if version != "" {
		query.Set("version", version)
	}
	var result types.EvaluatorDefinition
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/evaluators/"+url.PathEscape(id)+"?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) EvaluatorRuns(ctx context.Context, capsuleID string) ([]types.EvaluatorRun, error) {
	var result struct {
		Items []types.EvaluatorRun `json:"items"`
	}
	err := c.doJSON(ctx, http.MethodGet, "/api/v1/capsules/"+url.PathEscape(capsuleID)+"/evaluations", nil, &result)
	return result.Items, err
}

func (c *Client) doJSON(ctx context.Context, method, endpoint string, body, result any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.resolve(endpoint), reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	c.authorize(request)
	response, err := c.http.Do(request)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot contact Flight service", "start flight serve or check --url", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return decodeError(response)
	}
	if response.StatusCode == http.StatusNoContent || result == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(result)
}

func (c *Client) authorize(request *http.Request) {
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
}
func (c *Client) resolve(endpoint string) string {
	copyURL := *c.base
	copyURL.Path = strings.TrimSuffix(c.base.Path, "/") + strings.Split(endpoint, "?")[0]
	if index := strings.Index(endpoint, "?"); index >= 0 {
		copyURL.RawQuery = endpoint[index+1:]
	}
	return copyURL.String()
}

func decodeError(response *http.Response) error {
	var envelope struct {
		Error types.FlightError `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return types.NewError(types.ErrStorage, fmt.Sprintf("Flight service returned HTTP %d", response.StatusCode), "inspect service logs", err)
	}
	return &envelope.Error
}
