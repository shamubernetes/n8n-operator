# n8n-operator

A Kubernetes operator for managing n8n credentials and workflows declaratively.

## Overview

The n8n-operator enables GitOps-style management of n8n instances by providing Kubernetes Custom Resources for:

- **N8nCredential** - Manage n8n credentials from Kubernetes Secrets or 1Password
- **N8nWorkflow** - Deploy and manage n8n workflows from ConfigMaps or inline JSON

## Features

- 🔐 **Credential Management**: Create and sync n8n credentials from Kubernetes Secrets or 1Password Connect
- 📋 **Workflow Management**: Deploy workflows from ConfigMaps, with automatic credential ID injection
- 🔄 **Self-healing**: Continuously reconciles to ensure desired state matches actual state
- 🏷️ **GitOps Ready**: Define credentials and workflows as Kubernetes resources, synced by Flux/ArgoCD

## Installation

### Prerequisites

- Kubernetes cluster (1.26+)
- kubectl configured
- n8n instance with API access enabled

### Quick Install

```bash
# Install CRDs
kubectl apply -f https://raw.githubusercontent.com/shamubernetes/n8n-operator/main/config/crd/bases/n8n.n8n.io_n8ncredentials.yaml
kubectl apply -f https://raw.githubusercontent.com/shamubernetes/n8n-operator/main/config/crd/bases/n8n.n8n.io_n8nworkflows.yaml

# Install operator
kubectl apply -k https://github.com/shamubernetes/n8n-operator/config/default
```

### Using Kustomize

```yaml
# kustomization.yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/shamubernetes/n8n-operator/config/default?ref=v0.1.0
```

## Usage

### Prerequisites

Create a secret with your n8n API key:

```bash
kubectl create secret generic n8n-api-key \
  --from-literal=api-key=YOUR_N8N_API_KEY \
  -n services
```

### N8nCredential

Create credentials from a Kubernetes Secret:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nCredential
metadata:
  name: postgres-account
  namespace: services
spec:
  n8nInstance:
    url: http://n8n.services.svc:5678
    apiKeySecretRef:
      name: n8n-api-key
      key: api-key
  credentialName: "Postgres account"
  credentialType: postgres
  secretRef:
    name: postgres-credentials
  fieldMappings:
    host: POSTGRES_HOST
    user: POSTGRES_USER
    password: POSTGRES_PASSWORD
    database: POSTGRES_DATABASE
  data:
    port: "5432"
    ssl: "disable"
```

Create credentials from 1Password:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nCredential
metadata:
  name: webhook-auth
  namespace: services
spec:
  n8nInstance:
    url: http://n8n.services.svc:5678
    apiKeySecretRef:
      name: n8n-api-key
      key: api-key
  credentialName: "Webhook Auth"
  credentialType: httpHeaderAuth
  onePasswordRef:
    connectHost: http://onepassword-connect.op.svc:8080
    tokenSecretRef:
      name: op-connect-token
      key: token
    vaultId: "vault-uuid"
    itemId: "item-uuid"
    fieldMappings:
      value: token
  data:
    name: "Authorization"
```

### N8nWorkflow

Deploy a workflow from a ConfigMap:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nWorkflow
metadata:
  name: my-workflow
  namespace: services
spec:
  n8nInstance:
    url: http://n8n.services.svc:5678
    apiKeySecretRef:
      name: n8n-api-key
      key: api-key
  workflowName: "My Workflow"
  active: true
  sourceRef:
    kind: ConfigMap
    name: n8n-workflows
    key: my-workflow.json
  # Optional: Map credential names to N8nCredential resources
  credentialMappings:
    "Postgres account": postgres-account
```

Deploy a workflow with inline JSON:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nWorkflow
metadata:
  name: simple-workflow
  namespace: services
spec:
  n8nInstance:
    url: http://n8n.services.svc:5678
    apiKeySecretRef:
      name: n8n-api-key
      key: api-key
  workflowName: "Simple Workflow"
  active: false
  workflow:
    nodes:
      - parameters: {}
        id: manual-trigger
        name: Manual Trigger
        type: n8n-nodes-base.manualTrigger
        typeVersion: 1
        position: [0, 0]
    connections: {}
    settings:
      executionOrder: v1
```

## Status

Check the status of your resources:

```bash
# List credentials
kubectl get n8ncredentials -n services

# Get credential details
kubectl describe n8ncredential postgres-account -n services

# List workflows
kubectl get n8nworkflows -n services

# Get workflow details
kubectl describe n8nworkflow my-workflow -n services
```

## Development

### Prerequisites

- Go 1.23+
- Docker
- kubectl
- Access to a Kubernetes cluster

### Building

```bash
# Build the operator
make build

# Build Docker image
make docker-build IMG=ghcr.io/shamubernetes/n8n-operator:dev

# Run tests
make test
```

### Running locally

```bash
# Install CRDs
make install

# Run the operator locally (outside cluster)
make run
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                    │
│                                                          │
│  ┌──────────────────┐      ┌──────────────────┐        │
│  │   N8nCredential  │      │   N8nWorkflow    │        │
│  │   CRD Instance   │      │   CRD Instance   │        │
│  └────────┬─────────┘      └────────┬─────────┘        │
│           │                         │                   │
│           ▼                         ▼                   │
│  ┌──────────────────────────────────────────────┐      │
│  │              n8n-operator                     │      │
│  │  ┌─────────────────┐  ┌─────────────────┐   │      │
│  │  │   Credential    │  │    Workflow     │   │      │
│  │  │   Controller    │  │   Controller    │   │      │
│  │  └────────┬────────┘  └────────┬────────┘   │      │
│  └───────────┼────────────────────┼────────────┘      │
│              │                    │                    │
│              ▼                    ▼                    │
│  ┌──────────────────────────────────────────────┐     │
│  │                  n8n API                      │     │
│  │         (credentials, workflows)             │     │
│  └──────────────────────────────────────────────┘     │
│              │                                         │
│              ▼                                         │
│  ┌──────────────────┐    ┌──────────────────┐        │
│  │ Kubernetes       │    │  1Password       │        │
│  │ Secrets          │    │  Connect         │        │
│  └──────────────────┘    └──────────────────┘        │
└─────────────────────────────────────────────────────────┘
```

## License

Apache 2.0 - see [LICENSE](LICENSE)
