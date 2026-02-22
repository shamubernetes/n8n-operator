# n8n-operator

A Kubernetes operator for deploying and managing n8n workflow automation instances.

## Overview

The n8n-operator provides a complete solution for running n8n in Kubernetes:

- **N8nInstance** - Deploy and manage n8n applications (Deployment, Service, Ingress, PVC)
- **N8nCredential** - Manage n8n credentials from Kubernetes Secrets
- **N8nWorkflow** - Deploy n8n workflows from ConfigMaps or inline JSON

## Features

### Application Deployment
- 🚀 **Full n8n Deployment**: Deploy n8n with a single CRD
- 📊 **Queue Mode Support**: Horizontal scaling with Redis
- 💾 **Database Support**: PostgreSQL, MySQL, MariaDB, SQLite
- 🔐 **Encryption**: Automatic credential encryption key management
- 📈 **Metrics**: Prometheus metrics with ServiceMonitor
- 🌐 **Ingress**: Automatic Ingress creation with TLS

### Credential & Workflow Management
- 🔑 **Credential Sync**: Sync credentials from K8s Secrets to n8n
- 🔌 **External Secrets Compatible**: Works with any ESO backend
- 📋 **Workflow Deployment**: Deploy workflows declaratively
- 🔄 **Continuous Reconciliation**: Self-healing GitOps

## Installation

### Quick Install

```bash
# Install CRDs
kubectl apply -f https://raw.githubusercontent.com/shamubernetes/n8n-operator/main/config/crd/bases/n8n.n8n.io_n8ninstances.yaml
kubectl apply -f https://raw.githubusercontent.com/shamubernetes/n8n-operator/main/config/crd/bases/n8n.n8n.io_n8ncredentials.yaml
kubectl apply -f https://raw.githubusercontent.com/shamubernetes/n8n-operator/main/config/crd/bases/n8n.n8n.io_n8nworkflows.yaml

# Install operator
kubectl apply -k https://github.com/shamubernetes/n8n-operator/config/default
```

### Using Kustomize

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/shamubernetes/n8n-operator/config/default?ref=v0.3.0
```

## Usage

### N8nInstance - Deploy n8n

Basic deployment with PostgreSQL:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nInstance
metadata:
  name: n8n
  namespace: services
spec:
  replicas: 1
  image: docker.n8n.io/n8nio/n8n:latest

  database:
    type: postgresdb
    secretRef:
      name: n8n-postgres  # Keys: host, port, database, user, password

  encryption:
    keySecretRef:
      name: n8n-encryption
      key: key

  resources:
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: 1000m
      memory: 1Gi

  persistence:
    enabled: true
    size: 1Gi

  ingress:
    enabled: true
    className: nginx
    host: n8n.example.com
    tls:
      - hosts: [n8n.example.com]
        secretName: n8n-tls
```

Production HA setup with queue mode:

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nInstance
metadata:
  name: n8n-ha
spec:
  replicas: 3

  database:
    type: postgresdb
    secretRef:
      name: n8n-postgres
    ssl: true

  queue:
    enabled: true
    redis:
      secretRef:
        name: n8n-redis  # Keys: host, port, password

  webhook:
    url: https://n8n.example.com

  executions:
    mode: queue
    timeout: 3600
    pruneData: true
    pruneDataMaxAge: "168h"

  metrics:
    enabled: true
    serviceMonitor:
      enabled: true

  affinity:
    podAntiAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 100
          podAffinityTerm:
            labelSelector:
              matchLabels:
                app.kubernetes.io/name: n8n
            topologyKey: kubernetes.io/hostname
```

### N8nInstance Spec Reference

| Field | Type | Description |
|-------|------|-------------|
| `replicas` | int32 | Number of replicas (default: 1) |
| `image` | string | Container image (default: docker.n8n.io/n8nio/n8n:latest) |
| `imagePullPolicy` | string | Pull policy (Always/Never/IfNotPresent) |
| `imagePullSecrets` | []LocalObjectReference | Private registry secrets |
| `database` | DatabaseConfig | Database configuration (required) |
| `queue` | QueueConfig | Queue mode with Redis |
| `encryption` | EncryptionConfig | Encryption key for credentials |
| `webhook` | WebhookConfig | Webhook URL settings |
| `smtp` | SMTPConfig | Email configuration |
| `executions` | ExecutionsConfig | Execution settings |
| `logging` | LoggingConfig | Log level and output |
| `timezone` | string | Default timezone (default: UTC) |
| `service` | ServiceConfig | Kubernetes Service settings |
| `ingress` | IngressConfig | Kubernetes Ingress settings |
| `resources` | ResourceRequirements | CPU/Memory limits |
| `persistence` | PersistenceConfig | PVC settings |
| `metrics` | MetricsConfig | Prometheus metrics |
| `healthCheck` | HealthCheckConfig | Probe settings |
| `nodeSelector` | map[string]string | Node selection |
| `tolerations` | []Toleration | Pod tolerations |
| `affinity` | Affinity | Pod affinity rules |
| `extraEnv` | []EnvVar | Additional env vars |
| `extraVolumes` | []Volume | Additional volumes |
| `extraVolumeMounts` | []VolumeMount | Additional mounts |
| `initContainers` | []Container | Init containers |
| `sidecarContainers` | []Container | Sidecar containers |

### N8nCredential - Manage Credentials

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nCredential
metadata:
  name: postgres-account
spec:
  n8nInstance:
    url: http://n8n.services.svc:5678
    apiKeySecretRef:
      name: n8n-api-key
      key: api-key
  credentialName: "Postgres account"
  credentialType: postgres
  secretRef:
    name: postgres-credentials  # Can be managed by External Secrets
  fieldMappings:
    host: POSTGRES_HOST
    password: POSTGRES_PASSWORD
  data:
    port: "5432"
```

### N8nWorkflow - Deploy Workflows

```yaml
apiVersion: n8n.n8n.io/v1alpha1
kind: N8nWorkflow
metadata:
  name: my-workflow
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
    "Postgres account": postgres-account
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                        │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                    n8n-operator                         │ │
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐   │ │
│  │  │   Instance   │ │  Credential  │ │   Workflow   │   │ │
│  │  │  Controller  │ │  Controller  │ │  Controller  │   │ │
│  │  └──────┬───────┘ └──────┬───────┘ └──────┬───────┘   │ │
│  └─────────┼────────────────┼────────────────┼───────────┘ │
│            │                │                │              │
│            ▼                ▼                ▼              │
│  ┌──────────────┐    ┌──────────────┐  ┌──────────────┐   │
│  │ N8nInstance  │    │N8nCredential │  │ N8nWorkflow  │   │
│  │     CRD      │    │     CRD      │  │     CRD      │   │
│  └──────┬───────┘    └──────────────┘  └──────────────┘   │
│         │                                                   │
│         ▼                                                   │
│  ┌──────────────────────────────────────────────────────┐  │
│  │           Managed Resources                           │  │
│  │  ┌──────────┐ ┌─────────┐ ┌───────┐ ┌───────────┐   │  │
│  │  │Deployment│ │ Service │ │Ingress│ │    PVC    │   │  │
│  │  └──────────┘ └─────────┘ └───────┘ └───────────┘   │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Status

```bash
# Check n8n instance status
kubectl get n8ninstances
NAME   PHASE     REPLICAS   READY   URL                      AGE
n8n    Running   1          1       https://n8n.example.com  5m

# Check credentials
kubectl get n8ncredentials
NAME              CREDENTIAL         TYPE       ID    AGE
postgres-account  Postgres account   postgres   123   5m

# Check workflows
kubectl get n8nworkflows
NAME          WORKFLOW      ACTIVE   ID    AGE
my-workflow   My Workflow   true     456   5m
```

## Development

```bash
# Build
make build

# Run locally
make install  # Install CRDs
make run      # Run controller

# Build image
make docker-build IMG=ghcr.io/shamubernetes/n8n-operator:dev

# Run tests
go test -v ./pkg/...
```

## License

Apache 2.0 - see [LICENSE](LICENSE)
