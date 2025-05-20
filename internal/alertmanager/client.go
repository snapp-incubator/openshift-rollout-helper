package alertmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/models"
	"k8s.io/klog/v2"
)

type Client struct {
	baseURL        string
	authHeader     string
	httpClient     *http.Client
	activeSilences sync.Map
}

func NewClient(baseURL string, authToken string) *Client {
	client := &Client{
		baseURL:    baseURL,
		authHeader: authToken,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	return client
}

func (c *Client) CreateSilence(ctx context.Context, matchers models.Matchers, nodeName string) error {
	now := strfmt.DateTime(time.Now())
	endTime := strfmt.DateTime(time.Now().Add(90 * time.Minute))

	silence := models.Silence{
		Matchers:  matchers,
		StartsAt:  &now,
		EndsAt:    &endTime,
		CreatedBy: stringPtr("rollout-helper"),
		Comment:   stringPtr(fmt.Sprintf("Silencing alerts for node %s during rollout", nodeName)),
	}

	body, err := json.Marshal(silence)
	if err != nil {
		return fmt.Errorf("failed to marshal silence: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/api/v2/silences", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	klog.Infof("Created silence for node %s", nodeName)
	return nil
}

func (c *Client) DeleteSilence(ctx context.Context, nodeName string) error {
	// Get all silences
	silences, err := c.GetSilences(ctx)
	if err != nil {
		return fmt.Errorf("failed to get silences: %w", err)
	}

	// Find and delete silences created by rollout-helper for this node
	for _, silence := range silences {
		if silence.CreatedBy != nil && *silence.CreatedBy == "rollout-helper" {
			// Check if this silence is for our node, by checking its comment
			if strings.Contains(*silence.Comment, fmt.Sprintf(" %s ", nodeName)) {
				silenceID := silence.ID
				if err := c.DeleteSilenceID(ctx, silenceID); err != nil {
					return fmt.Errorf("failed to delete silence %s: %w", silenceID, err)
				}
			}
		}
	}

	return nil
}

func (c *Client) DeleteSilenceID(ctx context.Context, silenceID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", fmt.Sprintf("%s/api/v2/silence/%s", c.baseURL, silenceID), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	klog.Infof("Deleted silence %s", silenceID)
	return nil
}

// GetSilences fetches all silences from Alertmanager
func (c *Client) GetSilences(ctx context.Context) ([]models.PostableSilence, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/api/v2/silences", c.baseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var silences []models.PostableSilence
	if err := json.NewDecoder(resp.Body).Decode(&silences); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return silences, nil
}

// Helper functions for pointer types
func stringPtr(s string) *string     { return &s }
func boolPtr(b bool) *bool           { return &b }
func timePtr(t time.Time) *time.Time { return &t }
