package proxy

import (
	"fmt"

	"github.com/anup-a/artifacts/internal/variables"
)

// MaxSecretBytes bounds the variable values one call carries in total.
const MaxSecretBytes = 8 << 10

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
// and no value ever goes to a host its owner did not list.
//
// The error names the variable and host for the server log. It is not for
// the visitor, who is told only that the route is unavailable.
func (rt Route) Resolve(have map[string]Secret) (map[string]string, error) {
	out := map[string]string{}
	host := rt.Upstream.Hostname()
	total := 0
	for _, name := range rt.Vars() {
		s, ok := have[name]
		if !ok {
			return nil, fmt.Errorf("variable %s is not set", name)
		}
		if s.Err != nil {
			return nil, fmt.Errorf("variable %s cannot be decrypted", name)
		}
		if !variables.HostAllowed(host, s.AllowedUpstreams) {
			return nil, fmt.Errorf("variable %s is not allowed for %s", name, host)
		}
		if total += len(s.Value); total > MaxSecretBytes {
			return nil, fmt.Errorf("the route's variables are larger than %d bytes together", MaxSecretBytes)
		}
		out[name] = s.Value
	}
	return out, nil
}
