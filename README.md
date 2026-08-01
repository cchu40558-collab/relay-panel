# Relay Panel

Relay Panel is a self-hosted relay-line deployment panel. It provides a focused web interface for creating, applying, testing, and maintaining relay lines, including the associated Nginx and Xray runtime configuration.

This repository is an independent project. It does not publish Docker images, Windows packages, or 3x-ui releases.

## Install

Run as `root` on a supported Linux server:

```bash
PANEL_REPO_URL=https://github.com/cchu40558-collab/relay-panel.git bash <(curl -fsSL https://raw.githubusercontent.com/cchu40558-collab/relay-panel/v2.0.2/scripts/install-server.sh)
```

After installation, the script prints the panel address and initial credentials. Keep the active SSH session open until a new login is confirmed.

## Development

- Local development: [docs/local-dev.md](docs/local-dev.md)
- Server installation and upgrade: [docs/server-install.md](docs/server-install.md)

## Upstream Attribution

Relay Panel retains the portions of 3x-ui required for its existing Xray runtime and database integration. The upstream license is retained in [LICENSE](LICENSE).
