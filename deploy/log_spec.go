package deploy

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/multionlabs/caddy-rolling-deployment/docker"
	"go.uber.org/zap"
)

// specFields returns structured zap fields summarizing a ContainerSpec for logs.
func specFields(prefix string, spec *docker.ContainerSpec) []zap.Field {
	if spec == nil {
		return []zap.Field{zap.String(prefix, "<nil>")}
	}

	fields := []zap.Field{
		zap.String(prefix+".name", spec.Name),
	}

	if spec.Config != nil {
		fields = append(fields,
			zap.String(prefix+".image", spec.Config.Image),
			zap.Strings(prefix+".env", slices.Clone(spec.Config.Env)),
			zap.Strings(prefix+".cmd", slices.Clone(spec.Config.Cmd)),
			zap.Strings(prefix+".entrypoint", slices.Clone(spec.Config.Entrypoint)),
			zap.Any(prefix+".labels", cloneStringMap(spec.Config.Labels)),
			zap.Strings(prefix+".config_volumes", sortedKeys(spec.Config.Volumes)),
		)
	}

	if spec.HostConfig != nil {
		fields = append(fields,
			zap.Strings(prefix+".binds", slices.Clone(spec.HostConfig.Binds)),
			zap.Strings(prefix+".mounts", formatMounts(spec.HostConfig.Mounts)),
			zap.String(prefix+".network_mode", string(spec.HostConfig.NetworkMode)),
			zap.String(prefix+".restart_policy", string(spec.HostConfig.RestartPolicy.Name)),
			zap.Strings(prefix+".port_bindings", formatPortBindings(spec.HostConfig.PortBindings)),
		)
	}

	if spec.NetworkingConfig != nil {
		fields = append(fields,
			zap.Strings(prefix+".networks", sortedKeys(spec.NetworkingConfig.EndpointsConfig)),
			zap.Strings(prefix+".network_aliases", formatNetworkAliases(spec.NetworkingConfig)),
		)
	}

	return fields
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func formatPortBindings(bindings network.PortMap) []string {
	if len(bindings) == 0 {
		return nil
	}
	type item struct {
		key string
		val []network.PortBinding
	}
	items := make([]item, 0, len(bindings))
	for port, binds := range bindings {
		items = append(items, item{key: port.String(), val: binds})
	}
	slices.SortFunc(items, func(a, b item) int {
		return strings.Compare(a.key, b.key)
	})

	out := make([]string, 0, len(items))
	for _, it := range items {
		for _, b := range it.val {
			hostIP := ""
			if b.HostIP.IsValid() {
				hostIP = b.HostIP.String()
			}
			out = append(out, fmt.Sprintf("%s -> %s:%s", it.key, hostIP, b.HostPort))
		}
	}
	return out
}

func formatMounts(mounts []mount.Mount) []string {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		ro := ""
		if m.ReadOnly {
			ro = ":ro"
		}
		out = append(out, fmt.Sprintf("%s:%s:%s%s", m.Type, m.Source, m.Target, ro))
	}
	return out
}

func formatNetworkAliases(nc *network.NetworkingConfig) []string {
	if nc == nil || len(nc.EndpointsConfig) == 0 {
		return nil
	}
	keys := sortedKeys(nc.EndpointsConfig)
	out := make([]string, 0, len(keys))
	for _, name := range keys {
		ep := nc.EndpointsConfig[name]
		if ep == nil || len(ep.Aliases) == 0 {
			out = append(out, name+"=")
			continue
		}
		out = append(out, name+"="+strings.Join(ep.Aliases, ","))
	}
	return out
}
