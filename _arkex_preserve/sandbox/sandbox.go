// Package sandbox creates ephemeral, kernel-scoped containers per intent via docker CLI.
// Each container gets: read-only rootfs, 256 MiB scratch quota, proxy-only egress.
package sandbox

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	DefaultImage     = "ubuntu:22.04"
	ScratchMountPath = "/tmp/scratch"
)

// ContainerSpec defines parameters for one sandboxed intent execution.
type ContainerSpec struct {
	IntentID     string
	EngagementID string
	ScratchMB    int64
	Image        string
	ProxyAddr    string
	EnvVars      []string
	TargetPins   []string
}

// Container represents a live sandbox container.
type Container struct {
	ID         string
	IntentID   string
	ScratchDir string
}

// Manager creates and destroys sandboxed intent containers via docker CLI.
type Manager struct {
	proxyAddr   string
	scratchBase string
	networkID   string
}

func NewManager(proxyAddr, scratchBase, networkID string) (*Manager, error) {
	if err := os.MkdirAll(scratchBase, 0750); err != nil {
		return nil, fmt.Errorf("scratch dir: %w", err)
	}
	return &Manager{
		proxyAddr:   proxyAddr,
		scratchBase: scratchBase,
		networkID:   networkID,
	}, nil
}

// Create allocates an ephemeral container for the given intent.
func (m *Manager) Create(ctx context.Context, spec *ContainerSpec) (*Container, error) {
	if spec.ScratchMB <= 0 {
		spec.ScratchMB = 256
	}
	if spec.Image == "" {
		spec.Image = DefaultImage
	}

	scratchDir := filepath.Join(m.scratchBase, spec.IntentID)
	if err := os.MkdirAll(scratchDir, 0750); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}

	name := "arkex-intent-" + spec.IntentID[:min(8, len(spec.IntentID))]

	args := []string{
		"run", "-d",
		"--name", name,
		"--read-only",
		fmt.Sprintf("--memory=%dm", spec.ScratchMB*2),
		fmt.Sprintf("--memory-swap=%dm", spec.ScratchMB*2),
		"--cpus=0.5",
		"--tmpfs", fmt.Sprintf("%s:size=%dm,mode=750", ScratchMountPath, spec.ScratchMB),
		"-e", fmt.Sprintf("HTTP_PROXY=http://%s", m.proxyAddr),
		"-e", fmt.Sprintf("HTTPS_PROXY=http://%s", m.proxyAddr),
		"-e", "ARKEX_INTENT_ID=" + spec.IntentID,
		"-e", "ARKEX_ENGAGEMENT_ID=" + spec.EngagementID,
	}
	if m.networkID != "" {
		args = append(args, "--network", m.networkID)
	}
	for _, e := range spec.EnvVars {
		args = append(args, "-e", e)
	}
	for i, pin := range spec.TargetPins {
		args = append(args, "-e", fmt.Sprintf("ARKEX_TARGET_PIN_%d=%s", i, pin))
	}
	args = append(args, spec.Image, "sleep", "3600")

	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(string(out))
	slog.Debug("sandbox created", "container_id", containerID[:min(12, len(containerID))], "intent_id", spec.IntentID)
	return &Container{ID: containerID, IntentID: spec.IntentID, ScratchDir: scratchDir}, nil
}

// Exec runs a command inside the container.
func (m *Manager) Exec(ctx context.Context, containerID string, cmd []string) (stdout, stderr string, exitCode int, err error) {
	args := append([]string{"exec", containerID}, cmd...)
	out, execErr := exec.CommandContext(ctx, "docker", args...).Output()
	stdout = string(out)
	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			stderr = string(exitErr.Stderr)
			exitCode = exitErr.ExitCode()
			return stdout, stderr, exitCode, nil
		}
		return "", "", -1, execErr
	}
	return stdout, "", 0, nil
}

// Destroy stops and removes a container.
func (m *Manager) Destroy(ctx context.Context, containerID string) error {
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", containerID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %w: %s", err, out)
	}
	slog.Debug("sandbox destroyed", "container_id", containerID[:min(12, len(containerID))])
	return nil
}

