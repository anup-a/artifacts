package proxy

import (
	"github.com/anup-a/artifacts/internal/apierr"
	"github.com/anup-a/artifacts/internal/variables"
)

// Secret is one account variable opened for a call. Err is set instead of
// Value when the stored value could not be decrypted.
type Secret struct {
	Value            string
	AllowedUpstreams []string
	Err              error
}

// Resolve picks the route's variables out of the workspace's. A variable
// that is missing, unreadable, or not pinned to the route's upstream host
// stops the call: nothing is sent upstream with a hole where a key should be,
// and no value ever goes to a host its owner did not list. Errors name the
// variable, never its value.
func (rt Route) Resolve(have map[string]Secret) (map[string]string, *apierr.Error) {
	out := map[string]string{}
	host := rt.Upstream.Hostname()
	for _, name := range rt.Vars() {
		s, ok := have[name]
		if !ok {
			return nil, apierr.BadGateway("variable " + name + " is not set in this workspace")
		}
		if s.Err != nil {
			return nil, apierr.BadGateway("variable " + name + " cannot be read; set it again")
		}
		if !variables.HostAllowed(host, s.AllowedUpstreams) {
			return nil, apierr.Forbidden("variable " + name + " may not be sent to " + host + "; add it to the variable's allowed upstreams")
		}
		out[name] = s.Value
	}
	return out, nil
}
