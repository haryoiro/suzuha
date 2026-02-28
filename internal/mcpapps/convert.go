package mcpapps

import (
	"fmt"
	"os"
	"strings"

	"github.com/haryoiro/suzuha/internal/config"
)

// SelectPackage picks the best installable package from a server entry.
// Currently only npm+stdio is supported.
func SelectPackage(srv ServerJSON) (Package, error) {
	// Prefer npm+stdio.
	for _, pkg := range srv.Packages {
		if pkg.RegistryType == "npm" && pkg.Transport.Type == "stdio" {
			return pkg, nil
		}
	}
	// Fallback: any npm package.
	for _, pkg := range srv.Packages {
		if pkg.RegistryType == "npm" {
			return pkg, nil
		}
	}

	types := make([]string, 0, len(srv.Packages))
	for _, pkg := range srv.Packages {
		types = append(types, pkg.RegistryType+"/"+pkg.Transport.Type)
	}
	if len(types) == 0 {
		return Package{}, fmt.Errorf("server %q has no packages", srv.Name)
	}
	return Package{}, fmt.Errorf("no supported package type for %q (available: %s); only npm+stdio is supported",
		srv.Name, strings.Join(types, ", "))
}

// ToToolServer converts an MCP Registry Package to a config.ToolServer.
// userEnv provides user-supplied environment variable values.
func ToToolServer(serverName string, pkg Package, userEnv map[string]string) (config.ToolServer, error) {
	if pkg.RegistryType != "npm" {
		return config.ToolServer{}, fmt.Errorf("unsupported registry type %q; only npm is supported", pkg.RegistryType)
	}

	// Build command args.
	args := []string{"-y"}
	identifier := pkg.Identifier
	if pkg.Version != "" {
		identifier += "@" + pkg.Version
	}
	args = append(args, identifier)

	// Append package arguments.
	for _, arg := range pkg.PackageArguments {
		val := arg.Value
		if val == "" {
			val = arg.Default
		}
		if val == "" {
			continue
		}
		if arg.Type == "named" && arg.Name != "" {
			args = append(args, arg.Name, val)
		} else {
			args = append(args, val)
		}
	}

	// Build environment variables.
	env := make(map[string]string)
	var missing []string
	for _, ev := range pkg.EnvironmentVariables {
		// Priority: user-provided > process env > default
		if v, ok := userEnv[ev.Name]; ok {
			env[ev.Name] = v
		} else if v := os.Getenv(ev.Name); v != "" {
			env[ev.Name] = v
		} else if ev.Value != "" {
			env[ev.Name] = ev.Value
		} else if ev.IsRequired {
			missing = append(missing, ev.Name)
		}
	}
	// Also add any extra user-provided env vars not in the package spec.
	for k, v := range userEnv {
		if _, exists := env[k]; !exists {
			env[k] = v
		}
	}

	if len(missing) > 0 {
		return config.ToolServer{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	// Sanitize server name for use as config name (replace / with -)
	safeName := strings.ReplaceAll(serverName, "/", "-")

	return config.ToolServer{
		Name:      safeName,
		Type:      "mcp",
		Transport: pkg.Transport.Type,
		Command:   "npx",
		Args:      args,
		Env:       env,
	}, nil
}
