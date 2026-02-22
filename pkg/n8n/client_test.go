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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCredentialsHandlesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-N8N-API-KEY") != "test-key" {
			t.Fatalf("missing API key header")
		}

		cursor := r.URL.Query().Get("cursor")
		if cursor == "" {
			_ = json.NewEncoder(w).Encode(CredentialList{
				Data:       []Credential{{ID: "1", Name: "cred-a", Type: "postgres"}},
				NextCursor: "next-1",
			})
			return
		}

		if cursor != "next-1" {
			t.Fatalf("unexpected cursor %q", cursor)
		}

		_ = json.NewEncoder(w).Encode(CredentialList{
			Data: []Credential{{ID: "2", Name: "cred-b", Type: "httpHeaderAuth"}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	credentials, err := client.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("ListCredentials returned error: %v", err)
	}

	if len(credentials) != 2 {
		t.Fatalf("expected 2 credentials, got %d", len(credentials))
	}
	if credentials[0].Name != "cred-a" || credentials[1].Name != "cred-b" {
		t.Fatalf("unexpected credentials: %+v", credentials)
	}
}

func TestListWorkflowsHandlesPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursor := r.URL.Query().Get("cursor")
		if cursor == "" {
			_ = json.NewEncoder(w).Encode(WorkflowList{
				Data:       []Workflow{{ID: "11", Name: "wf-a"}},
				NextCursor: "next-2",
			})
			return
		}

		if cursor != "next-2" {
			t.Fatalf("unexpected cursor %q", cursor)
		}

		_ = json.NewEncoder(w).Encode(WorkflowList{
			Data: []Workflow{{ID: "12", Name: "wf-b"}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	workflows, err := client.ListWorkflows(context.Background())
	if err != nil {
		t.Fatalf("ListWorkflows returned error: %v", err)
	}

	if len(workflows) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(workflows))
	}
	if workflows[0].Name != "wf-a" || workflows[1].Name != "wf-b" {
		t.Fatalf("unexpected workflows: %+v", workflows)
	}
}
