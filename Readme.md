# Openshift Node Rollout Helper

A tool to automatically silence noisy alerts during node rollouts in OpenShift clusters. This helper monitors node annotations to detect machine config rollouts and automatically silences common scraping target alerts that are expected to be down during the rollout process.

## Features

- Monitors node annotations to detect machine config rollouts
- Automatically silences noisy alerts during node rollouts
- Supports all common scraping target alerts
- Can run with or without AlertManager integration
- Runs as a non-root user in containerized environments

### How Node Rollout Detection Works

The tool detects node rollouts by monitoring the `machineconfiguration.openshift.io/state` annotation on OpenShift nodes. Here's how it works:

1. **State Monitoring**: The tool polls nodes every 30 seconds to check their state annotations
2. **State Detection**:
   - When a node's state changes to `Working`, it indicates the node is being updated
   - When the state changes to `Done`, it indicates the update is complete
3. **Special Handling**:
   - If a node has the `wait-for-runc` taint after completion, the tool waits before considering the rollout complete
   - This ensures proper handling of the node's full lifecycle during updates

### Alerts Handled

The following alerts will get silenced during node rollouts:

- ScrapingTargetDown for cilium-agent
- ScrapingTargetDown for hubble-metrics
- ScrapingTargetDown for collector
- ScrapingTargetDown for dns-internal
- ScrapingTargetDown for machine-config-daemon
- ScrapingTargetDown for main-logging-fluentbit-monitor
- ScrapingTargetDown for spcld-network-vector-agent
- ScrapingTargetDown for node-exporter
- ScrapingTargetDown for kubelet

## Building

```bash
# Build the binary
go build -o rollout-helper

# Build the Docker image
docker build -t rollout-helper .
```

## Usage

### Running Locally

```bash
./rollout-helper \
  --alertmanager-url=http://alertmanager:9093 \
  --kubeconfig=/path/to/kubeconfig
```

### Running in Kubernetes

The Kubernetes manifests for running the rollout-helper in a cluster are available in the `manifests` directory.

## Configuration

### Command Line Flags

| Flag | Description | Required | Default |
|------|-------------|----------|---------|
| `--alertmanager-url` | URL of the AlertManager instance | Yes* | - |
| `--kubeconfig` | Path to kubeconfig file (only needed when running locally) | No | - |
| `--no-alertmanager` | Run without AlertManager, just log state events | No | false |

*Required unless `--no-alertmanager` is set to true

### Environment Variables

| Variable | Description | Required | Default |
|----------|-------------|----------|---------|
| `ALERTMNGR_TOKEN` | Authentication token for AlertManager | Yes* | - |

*Required unless `--no-alertmanager` is set to true