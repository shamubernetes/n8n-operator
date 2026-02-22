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

```bash
# Install CRDs
kubectl apply -f config/crd/bases/

# Install operator
kubectl apply -f config/manager/
```

## Usage

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
```

Create credentials from 1Password:

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
  onePasswordRef:
    connectHost: http://onepassword-connect.op.svc:8080
    tokenSecretRef:
      name: op-connect-token
      key: token
    vaultId: "abc123"
    itemId: "def456"
    fieldMappings:
      user: POSTGRES_USER
      password: POSTGRES_PASSWORD
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
  credentialMappings:
    "Postgres account": postgres-account  # Maps to N8nCredential resource
```

## Development

### Prerequisites

- Go 1.21+
- Docker
- kubectl
- Access to a Kubernetes cluster

### Building

```bash
# Build the operator
make build

# Build and push Docker image
make docker-build docker-push IMG=<registry>/n8n-operator:tag

# Generate manifests after changing types
make manifests generate
```

### Running locally

```bash
# Install CRDs
make install

# Run the operator locally
make run
```

### Running tests

```bash
make test
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
