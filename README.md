# gvm — Google VM Manager

A minimal CLI tool for managing Google Cloud Compute Engine instances from
your terminal. `gvm` can check instance status, start a stopped instance,
open an SSH session, or drop straight into a named `tmux` session — all with
a single command.

---

## Table of contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
  - [Install with `go install` (recommended)](#install-with-go-install-recommended)
  - [Build from source](#build-from-source)
  - [Pre-built binaries](#pre-built-binaries)
- [Authentication](#authentication)
- [Configuration](#configuration)
- [Usage](#usage)
  - [status](#status)
  - [start](#start)
  - [ssh](#ssh)
  - [tmux](#tmux)
- [Environment variables](#environment-variables)
- [Examples](#examples)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

| Requirement | Minimum version | Notes |
|---|---|---|
| [Go](https://go.dev/dl/) | 1.21 | Only needed when building from source |
| A GCP project | — | With Compute Engine API enabled |
| Google Cloud credentials | — | See [Authentication](#authentication) |
| `ssh` | any | Must be on your `PATH` |
| `tmux` | any | Required only for the `tmux` subcommand (remote host) |

---

## Installation

### Install with `go install` (recommended)

```bash
go install github.com/rodolfovillaruz/gvm-go@latest
```

The binary is placed in `$(go env GOPATH)/bin`.  
Make sure that directory is on your `PATH`:

```bash
# Add to ~/.bashrc, ~/.zshrc, or equivalent
export PATH="$PATH:$(go env GOPATH)/bin"
```

Verify the installation:

```bash
gvm-go --help   # or just run: gvm-go
```

You can rename the binary to `gvm` for convenience:

```bash
# Linux / macOS
cp "$(go env GOPATH)/bin/gvm-go" "$(go env GOPATH)/bin/gvm"
```

---

### Build from source

```bash
# 1. Clone the repository
git clone https://github.com/rodolfovillaruz/gvm-go.git
cd gvm-go

# 2. Download dependencies
go mod download

# 3a. Install to GOPATH/bin
go install .

# 3b. — or — build a local binary
go build -o gvm .
```

To cross-compile for a different platform:

```bash
# Linux (amd64)
GOOS=linux  GOARCH=amd64 go build -o gvm-linux-amd64 .

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o gvm-darwin-arm64 .

# Windows (amd64)
GOOS=windows GOARCH=amd64 go build -o gvm-windows-amd64.exe .
```

---

### Pre-built binaries

Download the latest release for your platform from the
[Releases](https://github.com/rodolfovillaruz/gvm-go/releases) page,
make it executable, and move it somewhere on your `PATH`:

```bash
# Example — Linux amd64
curl -L https://github.com/rodolfovillaruz/gvm-go/releases/latest/download/gvm-linux-amd64 \
  -o /usr/local/bin/gvm
chmod +x /usr/local/bin/gvm
```

---

## Authentication

`gvm` uses [Application Default Credentials (ADC)](https://cloud.google.com/docs/authentication/application-default-credentials).
At least one of the following must be satisfied before running any subcommand.

### Option A — gcloud CLI (recommended for local development)

```bash
gcloud auth application-default login
```

This writes credentials to:

| OS | Path |
|---|---|
| Linux / macOS | `~/.config/gcloud/application_default_credentials.json` |
| Windows | `%LOCALAPPDATA%\gcloud\application_default_credentials.json` |

### Option B — Service account key file

```bash
export GOOGLE_APPLICATION_CREDENTIALS="/path/to/service-account-key.json"
```

The service account must have at minimum the **Compute Instance Admin (v1)**
(`roles/compute.instanceAdmin.v1`) role on the project.

---

## Configuration

All configuration is done through environment variables.  
The simplest approach is to export them in your shell profile or a `.env` file.

```bash
# ~/.bashrc or ~/.zshrc
export GOOGLE_CLOUD_PROJECT="my-gcp-project"
export GVM_INSTANCE="my-dev-vm"
export GVM_USER="alice"
```

---

## Usage

```
gvm <start|status|ssh|tmux> [args...]
```

### status

Print the current power state of the instance (`RUNNING` or `STOPPED`).

```bash
gvm status
```

### start

Start the instance (if not already running) and open an SSH session once
port 22 becomes reachable. Any extra arguments are forwarded to `ssh`.

```bash
gvm start
gvm start -L 8080:localhost:8080   # with SSH flags
```

The command waits up to **180 seconds** by default.  
Override with `GVM_START_TIMEOUT` (in seconds):

```bash
GVM_START_TIMEOUT=300 gvm start
```

### ssh

Open an SSH session to a **running** instance. Extra arguments are forwarded
directly to the underlying `ssh` call.

```bash
gvm ssh
gvm ssh -L 5432:localhost:5432     # port-forward
gvm ssh -- ls -lah /var/log        # run a remote command
```

### tmux

SSH into the instance and attach to (or create) a named `tmux` session.

```bash
gvm tmux <session-name>

# Examples
gvm tmux main
gvm tmux dev
```

---

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `GVM_INSTANCE` | **Yes** | — | Name of the Compute Engine instance |
| `GOOGLE_CLOUD_PROJECT` | **Yes** | — | GCP project ID |
| `GVM_USER` | For `start`, `ssh`, `tmux` | — | SSH username on the remote instance |
| `GOOGLE_APPLICATION_CREDENTIALS` | No | — | Path to a service account JSON key file |
| `GVM_START_TIMEOUT` | No | `180` | Seconds to wait for SSH after starting an instance |

---

## Examples

```bash
# Check if your dev VM is running
gvm status

# Start it and jump in
gvm start

# SSH in directly (instance must already be running)
gvm ssh

# Forward a remote Postgres port locally
gvm ssh -L 5432:localhost:5432

# Attach to (or create) a tmux session called "work"
gvm tmux work

# Use a different instance without changing your profile
GVM_INSTANCE=staging-vm gvm status
```

---

## Troubleshooting

**`GVM_INSTANCE environment variable is not set`**  
Export the variable before running: `export GVM_INSTANCE="my-vm"`

**`No Google Cloud credentials found`**  
Run `gcloud auth application-default login` or set
`GOOGLE_APPLICATION_CREDENTIALS` to the path of a valid key file.

**`instance not found in project`**  
Verify that `GVM_INSTANCE` and `GOOGLE_CLOUD_PROJECT` are correct, and that
the Compute Engine API is enabled:

```bash
gcloud services enable compute.googleapis.com --project "$GOOGLE_CLOUD_PROJECT"
```

**`timed out waiting for instance to accept SSH`**  
The VM may be slow to boot, or port 22 may be blocked by a firewall rule.
Increase the timeout with `GVM_START_TIMEOUT=300` and verify your VPC firewall
allows ingress on TCP 22.

**`ssh: command not found`**  
Install OpenSSH and ensure the `ssh` binary is on your `PATH`.
