package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/lease"
)

func TestR13OwnerSeamMatches(t *testing.T) {
	host := "https://billing.example"
	jkt := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	cases := []struct {
		row, typed string
		ttl        time.Duration
		pending    lease.Pending
		want       bool
	}{
		{"R13/origin-request-typed-origin-match", host, 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "origin", URL: host}}}, true},
		{"R13/exact-root-request-typed-root-match", host + "/", 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "exact", URL: host + "/"}}}, true},
		{"R13/origin-request-typed-exact-root-mismatch", host + "/", 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "origin", URL: host}}}, false},
		{"R13/exact-root-request-typed-origin-mismatch", host, 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "exact", URL: host + "/"}}}, false},
		{"R13/ttl-900s-equals-15m-match", host, 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "origin", URL: host}}}, true},
		{"R13/ttl-901s-mismatch", host, 900 * time.Second, lease.Pending{JKT: jkt, TTL: 901, Allow: []lease.Rule{{Kind: "origin", URL: host}}}, false},
		{"R13/jkt-mismatch", host, 15 * time.Minute, lease.Pending{JKT: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB", TTL: 900, Allow: []lease.Rule{{Kind: "origin", URL: host}}}, false},
		{"R13/equal-text-exact-vs-path-prefix", host + "/a/**", 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "exact", URL: host + "/a"}}}, false},
		{"R13/equal-text-exact-root-vs-path-prefix", host + "/**", 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "exact", URL: host + "/"}}}, false},
		{"R13/percent-20-exact-match", `https://billing.example/my%20report/`, 15 * time.Minute, lease.Pending{JKT: jkt, TTL: 900, Allow: []lease.Rule{{Kind: "exact", URL: `https://billing.example/my%20report/`}}}, true},
	}
	for _, c := range cases {
		f := ownerFlags{op: "review", to: "jkt:" + jkt, ttl: c.ttl, scope: stringList{c.typed}}
		rules, err := f.rules()
		ruleErr := "nil"
		if err != nil {
			ruleErr = "err"
		}
		match := false
		kind := "none"
		if err == nil {
			match = f.matches(c.pending, rules)
			if len(rules) > 0 {
				kind = rules[0].Kind
			}
		}
		fmt.Printf("ci-proof lease-baseline %s seam=ownerFlags match=%t want=%t rule_err=%s kind=%s\n", c.row, match, c.want, ruleErr, kind)
		if err == nil && match != c.want {
			t.Fatalf("%s: match=%t want=%t", c.row, match, c.want)
		}
	}
	for _, bad := range []struct{ raw, class string }{
		{"https://Host/", "host-caps"},
		{"https://host:443/", "default-port"},
		{"https://billing.example/\u212a", "unicode"},
	} {
		_, err := ownerRule(bad.raw)
		fmt.Printf("ci-proof lease-baseline R13/non-canonical-typed-rejected class=%s rejected=%t\n", bad.class, err != nil)
		if err == nil {
			t.Fatalf("expected reject for %s", bad.class)
		}
	}
	_, err := ownerRule("https://billing.example/my report/")
	fmt.Printf("ci-proof lease-baseline R13/raw-space-rule rejected=%t\n", err != nil)
}
