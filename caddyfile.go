package rolling_deployment

import (
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
func (m *Middleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // consume directive name

	for d.NextBlock(0) {
		switch d.Val() {
		case "secret":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.Secret = d.Val()
		case "docker_hosts":
			m.DockerHosts = append(m.DockerHosts, d.RemainingArgs()...)

			if len(m.DockerHosts) == 0 {
				return d.ArgErr()
			}
		default:
			return d.Errf("unrecognized subdirective: %s", d.Val())
		}
	}

	return nil
}

// parseCaddyfile unmarshals tokens from h into a new Middleware.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var m Middleware
	err := m.UnmarshalCaddyfile(h.Dispenser)
	return m, err
}
