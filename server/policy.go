package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Policy applies an ordered middleware chain to an exact path or a subtree.
type Policy struct {
	pattern  string
	handlers []fiber.Handler
}

// Protect declares a path policy. A trailing /* includes the base path and its
// descendants. Exact paths override matching subtree policies.
func Protect(pattern string, handlers ...fiber.Handler) Policy {
	return Policy{pattern: pattern, handlers: append([]fiber.Handler(nil), handlers...)}
}

type policyRule struct {
	prefix   string
	subtree  bool
	handlers []fiber.Handler
}

type policySet struct {
	rules  []policyRule
	stages int
}

func compilePolicies(policies []Policy) (policySet, error) {
	set := policySet{rules: make([]policyRule, 0, len(policies))}
	seen := make(map[string]bool, len(policies))
	for _, p := range policies {
		pattern := p.pattern
		subtree := strings.HasSuffix(pattern, "/*")
		base := pattern
		if subtree {
			base = strings.TrimSuffix(base, "/*")
		}
		if base == "" && subtree {
			base = "/"
		}
		if !validPolicyPath(base) || (!subtree && base != "/" && strings.HasSuffix(base, "/")) {
			return policySet{}, fmt.Errorf("server: invalid policy path %q: use an absolute literal path or a trailing /*", pattern)
		}
		if seen[pattern] {
			return policySet{}, fmt.Errorf("server: duplicate policy %q", pattern)
		}
		seen[pattern] = true
		if len(p.handlers) == 0 {
			return policySet{}, fmt.Errorf("server: policy %q has no middleware", pattern)
		}
		for _, h := range p.handlers {
			if h == nil {
				return policySet{}, fmt.Errorf("server: policy %q has nil middleware", pattern)
			}
		}
		handlers := append([]fiber.Handler(nil), p.handlers...)
		set.rules = append(set.rules, policyRule{prefix: base, subtree: subtree, handlers: handlers})
		if len(handlers) > set.stages {
			set.stages = len(handlers)
		}
	}
	sort.Slice(set.rules, func(i, j int) bool {
		a, b := set.rules[i], set.rules[j]
		if len(a.prefix) != len(b.prefix) {
			return len(a.prefix) > len(b.prefix)
		}
		return !a.subtree && b.subtree
	})
	return set, nil
}

func validPolicyPath(p string) bool {
	if p == "" || p[0] != '/' || strings.Contains(p, "//") {
		return false
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return false
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~') {
				return false
			}
		}
	}
	return true
}

func (s policySet) match(path string) []fiber.Handler {
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	for _, rule := range s.rules {
		if path == rule.prefix || (rule.subtree && (rule.prefix == "/" || strings.HasPrefix(path, rule.prefix+"/"))) {
			return rule.handlers
		}
	}
	return nil
}

func (s policySet) middleware() []fiber.Handler {
	stages := make([]fiber.Handler, s.stages)
	for i := range stages {
		index := i
		stages[i] = func(c fiber.Ctx) error {
			chain := s.match(c.Path())
			if index >= len(chain) {
				return c.Next()
			}
			if index == 0 {
				c.Set(fiber.HeaderCacheControl, "private, no-store")
			}
			return chain[index](c)
		}
	}
	return stages
}
