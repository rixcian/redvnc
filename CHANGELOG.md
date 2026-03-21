# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- JSON configuration file support (`--config` flag) with environment variable overrides
- Dockerfile and Docker Compose for containerized deployment
- Version variable with build-time override support
- Authentication rate limiting with escalating lockouts
- Structured logging via `log/slog` (text and JSON formats)
- Health check endpoints (`/health`, `/ready`)
- Graceful shutdown with configurable drain timeout
- Browser auto-reconnect with exponential backoff and jitter
- VNC Authentication (security type 2) with DES challenge-response
- TLS support with minimum TLS 1.2
- WebSocket proxy with file upload, clipboard sync
- Windows screen capture via DXGI Desktop Duplication
- Windows input injection via SendInput
- Linux screen capture via X11/XShm
- Linux input injection via XTest
- Browser client unit tests (rfb-writer, rfb-parser, framebuffer, encodings)
- Go integration tests for VNC auth and client disconnect stability
- CI pipeline with Go tests, vet, and web client typecheck/tests

## [0.1.0] - 2026-03-21

Initial pre-release.
