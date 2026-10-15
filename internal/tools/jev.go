package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// defaultJevEndpoint is the TypeSafe systemone URL. Config duplicates
	// this string so config does not import tools.
	defaultJevEndpoint = "https://api.typesafe.ai/v1/systemone"
	defaultJevTimeout  = 2 * time.Second
	jevModelName       = "jev-preview"
	typeSafeAPIKeyEnv  = "TYPESAFE_API_KEY"

	jevNeedsConfirmInstructions = "An AI coding agent wants to run the bash command in `command` inside the user's project. Is this command dangerous enough (destructive, irreversible, data loss, security risk, or affects things outside the project) that the agent should ask the user for confirmation before running it?"
)

var (
	errJevUnconfigured = errors.New("jev unconfigured")
	errJevTimeout      = errors.New("jev timeout")
	errJevUnavailable  = errors.New("jev unavailable")
	errJevResponse     = errors.New("jev bad response")
	errJevRedirect     = errors.New("jev redirect")
)

func errorsIsTimeout(err error) bool {
	return errors.Is(err, errJevTimeout) || errors.Is(err, context.DeadlineExceeded)
}

type jevScorer struct {
	endpoint string
	timeout  time.Duration
	http     *http.Client
}

func newJevScorer(endpoint string, timeout time.Duration, client *http.Client) *jevScorer {
	if timeout <= 0 {
		timeout = defaultJevTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if client.CheckRedirect == nil {
		base := *client
		base.CheckRedirect = refuseRedirect
		if base.Timeout <= 0 {
			base.Timeout = timeout
		}
		client = &base
	}
	return &jevScorer{endpoint: endpoint, timeout: timeout, http: client}
}

func refuseRedirect(*http.Request, []*http.Request) error {
	return errJevRedirect
}

func (s *jevScorer) Score(ctx context.Context, command, cwd, goos string) (float64, error) {
	// The key lives only in this stack frame and in the request header.
	// It is not stored on the scorer, not written to config, and not
	// included in any error string returned from this method.
	key := strings.TrimSpace(os.Getenv(typeSafeAPIKeyEnv))
	if key == "" {
		return 0, errJevUnconfigured
	}
	endpoint, err := sanitizeEndpoint(s.endpoint)
	if err != nil {
		return 0, errJevUnavailable
	}
	body, err := json.Marshal(jevRequest(command, cwd, goos))
	if err != nil {
		return 0, errJevUnavailable
	}
	reqCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, errJevUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return 0, errJevTimeout
		}
		return 0, errJevUnavailable
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, errJevUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return 0, errJevUnavailable
	}
	noul, err := parseJevNoul(payload)
	if err != nil {
		return 0, errJevResponse
	}
	return noul, nil
}

func sanitizeEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errJevUnavailable
	}
	if u.User != nil {
		return "", errJevUnavailable
	}
	return u.String(), nil
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevRequestBody struct {
	Model     string                 `json:"model"`
	State     map[string]string      `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

func jevRequest(command, cwd, goos string) jevRequestBody {
	return jevRequestBody{
		Model: jevModelName,
		State: map[string]string{
			"command": command,
			"cwd":     cwd,
			"os":      goos,
		},
		Questions: map[string]jevQuestion{
			"needs_confirm": {
				Type:         "noul",
				Instructions: jevNeedsConfirmInstructions,
				Criteria: map[string]string{
					"true":  "Dangerous: require user confirmation",
					"false": "Safe routine command: run without asking",
				},
			},
		},
	}
}

type jevResponseBody struct {
	Answers map[string]struct {
		Type string   `json:"type"`
		Noul *float64 `json:"noul"`
	} `json:"answers"`
}

func parseJevNoul(payload []byte) (float64, error) {
	var resp jevResponseBody
	if err := json.Unmarshal(payload, &resp); err != nil {
		return 0, errJevResponse
	}
	answer, ok := resp.Answers["needs_confirm"]
	if !ok || answer.Type != "noul" || answer.Noul == nil {
		return 0, errJevResponse
	}
	noul := *answer.Noul
	if math.IsNaN(noul) || math.IsInf(noul, 0) || noul < 0 || noul > 1 {
		return 0, errJevResponse
	}
	return noul, nil
}
