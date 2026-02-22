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

package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is an n8n API client
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a new n8n API client
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Credential represents an n8n credential
type Credential struct {
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name"`
	Type      string                 `json:"type"`
	Data      map[string]interface{} `json:"data,omitempty"`
	CreatedAt string                 `json:"createdAt,omitempty"`
	UpdatedAt string                 `json:"updatedAt,omitempty"`
}

// CredentialList represents the response from listing credentials
type CredentialList struct {
	Data       []Credential `json:"data"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

// Workflow represents an n8n workflow
type Workflow struct {
	ID          string                   `json:"id,omitempty"`
	Name        string                   `json:"name"`
	Active      bool                     `json:"active,omitempty"`
	Nodes       []map[string]interface{} `json:"nodes,omitempty"`
	Connections map[string]interface{}   `json:"connections,omitempty"`
	Settings    map[string]interface{}   `json:"settings,omitempty"`
	CreatedAt   string                   `json:"createdAt,omitempty"`
	UpdatedAt   string                   `json:"updatedAt,omitempty"`
}

// WorkflowList represents the response from listing workflows
type WorkflowList struct {
	Data       []Workflow `json:"data"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// APIError represents an error from the n8n API
type APIError struct {
	Message string `json:"message"`
	Code    int    `json:"-"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("n8n API error (code %d): %s", e.Code, e.Message)
}

func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-N8N-API-KEY", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
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
		apiErr.Code = resp.StatusCode
		return nil, &apiErr
	}

	return respBody, nil
}

// ListCredentials lists all credentials
func (c *Client) ListCredentials(ctx context.Context) ([]Credential, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/api/v1/credentials", nil)
	if err != nil {
		return nil, err
	}

	var list CredentialList
	if err := json.Unmarshal(respBody, &list); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}

	return list.Data, nil
}

// GetCredentialByName finds a credential by name
func (c *Client) GetCredentialByName(ctx context.Context, name string) (*Credential, error) {
	creds, err := c.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}

	for _, cred := range creds {
		if cred.Name == name {
			return &cred, nil
		}
	}

	return nil, nil
}

// CreateCredential creates a new credential
func (c *Client) CreateCredential(ctx context.Context, cred *Credential) (*Credential, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/api/v1/credentials", cred)
	if err != nil {
		return nil, err
	}

	var created Credential
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, fmt.Errorf("failed to unmarshal created credential: %w", err)
	}

	return &created, nil
}

// UpdateCredential updates an existing credential
func (c *Client) UpdateCredential(ctx context.Context, id string, cred *Credential) (*Credential, error) {
	respBody, err := c.doRequest(ctx, http.MethodPatch, "/api/v1/credentials/"+id, cred)
	if err != nil {
		return nil, err
	}

	var updated Credential
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, fmt.Errorf("failed to unmarshal updated credential: %w", err)
	}

	return &updated, nil
}

// DeleteCredential deletes a credential
func (c *Client) DeleteCredential(ctx context.Context, id string) error {
	_, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/credentials/"+id, nil)
	return err
}

// ListWorkflows lists all workflows
func (c *Client) ListWorkflows(ctx context.Context) ([]Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/api/v1/workflows", nil)
	if err != nil {
		return nil, err
	}

	var list WorkflowList
	if err := json.Unmarshal(respBody, &list); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflows: %w", err)
	}

	return list.Data, nil
}

// GetWorkflow gets a workflow by ID
func (c *Client) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodGet, "/api/v1/workflows/"+id, nil)
	if err != nil {
		return nil, err
	}

	var wf Workflow
	if err := json.Unmarshal(respBody, &wf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workflow: %w", err)
	}

	return &wf, nil
}

// GetWorkflowByName finds a workflow by name
func (c *Client) GetWorkflowByName(ctx context.Context, name string) (*Workflow, error) {
	workflows, err := c.ListWorkflows(ctx)
	if err != nil {
		return nil, err
	}

	for _, wf := range workflows {
		if wf.Name == name {
			return &wf, nil
		}
	}

	return nil, nil
}

// CreateWorkflow creates a new workflow
func (c *Client) CreateWorkflow(ctx context.Context, wf *Workflow) (*Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/api/v1/workflows", wf)
	if err != nil {
		return nil, err
	}

	var created Workflow
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, fmt.Errorf("failed to unmarshal created workflow: %w", err)
	}

	return &created, nil
}

// UpdateWorkflow updates an existing workflow
func (c *Client) UpdateWorkflow(ctx context.Context, id string, wf *Workflow) (*Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodPut, "/api/v1/workflows/"+id, wf)
	if err != nil {
		return nil, err
	}

	var updated Workflow
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, fmt.Errorf("failed to unmarshal updated workflow: %w", err)
	}

	return &updated, nil
}

// DeleteWorkflow deletes a workflow
func (c *Client) DeleteWorkflow(ctx context.Context, id string) error {
	_, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/workflows/"+id, nil)
	return err
}

// ActivateWorkflow activates a workflow
func (c *Client) ActivateWorkflow(ctx context.Context, id string) (*Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/api/v1/workflows/"+id+"/activate", nil)
	if err != nil {
		return nil, err
	}

	var wf Workflow
	if err := json.Unmarshal(respBody, &wf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal activated workflow: %w", err)
	}

	return &wf, nil
}

// DeactivateWorkflow deactivates a workflow
func (c *Client) DeactivateWorkflow(ctx context.Context, id string) (*Workflow, error) {
	respBody, err := c.doRequest(ctx, http.MethodPost, "/api/v1/workflows/"+id+"/deactivate", nil)
	if err != nil {
		return nil, err
	}

	var wf Workflow
	if err := json.Unmarshal(respBody, &wf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deactivated workflow: %w", err)
	}

	return &wf, nil
}
