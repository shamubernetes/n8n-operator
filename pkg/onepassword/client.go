/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package onepassword

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a 1Password Connect API client
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new 1Password Connect client
func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Vault represents a 1Password vault
type Vault struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Item represents a 1Password item
type Item struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Vault    Vault   `json:"vault"`
	Fields   []Field `json:"fields"`
}

// Field represents a field in a 1Password item
type Field struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Label   string `json:"label"`
	Value   string `json:"value,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// APIError represents an error from the 1Password Connect API
type APIError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("1Password Connect API error (status %d): %s", e.Status, e.Message)
}

func (c *Client) doRequest(ctx context.Context, method, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err != nil {
			apiErr.Message = string(respBody)
		}
		apiErr.Status = resp.StatusCode
		return nil, &apiErr
	}

	return respBody, nil
}

// ListVaults lists all accessible vaults
func (c *Client) ListVaults(ctx context.Context) ([]Vault, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/v1/vaults")
	if err != nil {
		return nil, err
	}

	var vaults []Vault
	if err := json.Unmarshal(respBody, &vaults); err != nil {
		return nil, fmt.Errorf("failed to unmarshal vaults: %w", err)
	}

	return vaults, nil
}

// GetItem gets an item from a vault
func (c *Client) GetItem(ctx context.Context, vaultID, itemID string) (*Item, error) {
	path := fmt.Sprintf("/v1/vaults/%s/items/%s", vaultID, itemID)
	respBody, err := c.doRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, err
	}

	var item Item
	if err := json.Unmarshal(respBody, &item); err != nil {
		return nil, fmt.Errorf("failed to unmarshal item: %w", err)
	}

	return &item, nil
}

// GetFieldValue gets the value of a specific field from an item
func (c *Client) GetFieldValue(ctx context.Context, vaultID, itemID, fieldLabel string) (string, error) {
	item, err := c.GetItem(ctx, vaultID, itemID)
	if err != nil {
		return "", err
	}

	for _, field := range item.Fields {
		if field.Label == fieldLabel {
			return field.Value, nil
		}
	}

	return "", fmt.Errorf("field with label %q not found in item", fieldLabel)
}

// GetFieldValues gets multiple field values from an item as a map
func (c *Client) GetFieldValues(ctx context.Context, vaultID, itemID string, fieldLabels []string) (map[string]string, error) {
	item, err := c.GetItem(ctx, vaultID, itemID)
	if err != nil {
		return nil, err
	}

	// Create a map of label -> value for quick lookup
	fieldMap := make(map[string]string)
	for _, field := range item.Fields {
		fieldMap[field.Label] = field.Value
	}

	// Extract requested fields
	result := make(map[string]string)
	for _, label := range fieldLabels {
		if value, ok := fieldMap[label]; ok {
			result[label] = value
		}
	}

	return result, nil
}

// GetAllFieldValues gets all field values from an item as a map
func (c *Client) GetAllFieldValues(ctx context.Context, vaultID, itemID string) (map[string]string, error) {
	item, err := c.GetItem(ctx, vaultID, itemID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, field := range item.Fields {
		if field.Value != "" {
			result[field.Label] = field.Value
		}
	}

	return result, nil
}
