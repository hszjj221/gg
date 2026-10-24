package app

import "github.com/hszjj221/gg/internal/proactive"

// NewProactivePolicy is the statically linked delivery-policy factory.
// It only validates local delivery settings; callers retain scheduling,
// output, and approval ownership. Construction starts no background work.
func NewProactivePolicy(quietHours string) (proactive.Policy, error) {
	policy := proactive.Policy{QuietHours: quietHours}
	return policy, policy.Validate()
}
