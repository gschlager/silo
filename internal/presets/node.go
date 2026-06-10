package presets

import "gopkg.in/yaml.v3"

// nodePreset installs Node.js and corepack from the distro repos.
//
//	use:
//	  node:
//
// corepack manages pnpm/yarn from the project's packageManager field, so a
// specific Node version is left to the distro package (Fedora ships a single
// nodejs). Projects needing a pinned Node can still add commands in setup:.
type nodePreset struct{}

func (nodePreset) SetupCommands(_ yaml.Node, _ string) ([]string, error) {
	return []string{
		"sudo dnf install -y nodejs npm",
		"sudo npm install -g corepack",
		"corepack enable",
	}, nil
}
