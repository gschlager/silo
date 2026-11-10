package provision

import (
	"strings"

	"github.com/gschlager/silo/internal/config"
	"github.com/gschlager/silo/internal/presets"
)

// LoadConfig loads and merges the global and project configuration for the
// project in dir, then applies any per-container mode overrides from silo
// mode state.
func LoadConfig(cwd string) (*config.MergedConfig, error) {

	global, err := config.LoadGlobalConfig()
	if err != nil {
		return nil, err
	}

	project, err := config.LoadProjectConfig(cwd)
	if err != nil {
		return nil, err
	}

	var use config.UseList
	if project != nil {
		use = project.Use
	}
	if err := config.ValidateEnv(global, project, use); err != nil {
		return nil, err
	}

	merged := config.Merge(global, project, cwd)
	containerName, err := config.AssignContainerName(cwd)
	if err != nil {
		return nil, err
	}
	merged.ContainerName = containerName
	merged.ProjectKey = strings.TrimPrefix(containerName, "silo-")

	// Use the shell the container was actually provisioned with, which may differ
	// from the configured one if it could not be installed and silo fell back to
	// bash. Keeps enter/run/daemons consistent with provisioning.
	if shell := config.LoadContainerShell(merged.ContainerName); shell != "" {
		merged.Shell = shell
	}

	// Expand built-in presets (use:) into setup commands, prepended so runtimes
	// and services are ready before the project's own setup (e.g. bundle install).
	presetCmds, err := presets.Expand(merged.Use, merged.Shell, merged.Presets)
	if err != nil {
		return nil, err
	}
	merged.Setup = append(presetCmds, merged.Setup...)

	// Apply mode overrides from state file.
	modes, err := config.LoadModeState(merged.ContainerName)
	if err != nil {
		return nil, err
	}
	for agentName, mode := range modes {
		if agent, ok := merged.Agents[agentName]; ok {
			agent.Mode = mode
			merged.Agents[agentName] = agent
		}
	}

	return merged, nil
}
