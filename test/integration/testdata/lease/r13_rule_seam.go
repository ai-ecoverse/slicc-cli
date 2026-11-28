package lease

import (
	"fmt"
	"testing"
)

func TestLeaseBaselineR13RuleSeam(t *testing.T) {
	host := "https://billing.example"
	for _, c := range []struct {
		row  string
		rule Rule
		raw  string
	}{
		{"R13/rule-exact-a", Rule{Kind: "exact", URL: host + "/a"}, host + "/a"},
		{"R13/rule-path-prefix-a", Rule{Kind: "path-prefix", URL: host + "/a"}, host + "/a/b"},
		{"R13/rule-exact-root", Rule{Kind: "exact", URL: host + "/"}, host + "/"},
		{"R13/rule-path-prefix-root", Rule{Kind: "path-prefix", URL: host + "/"}, host + "/x"},
		{"R13/rule-percent-20", Rule{Kind: "exact", URL: host + "/my%20report/"}, host + "/my%20report/"},
		{"R13/rule-raw-space", Rule{Kind: "exact", URL: host + "/my report/"}, host + "/my report/"},
	} {
		ok := c.rule.Valid(nil)
		matched := matches([]Rule{c.rule}, c.raw, nil)
		fmt.Printf("ci-proof lease-baseline %s valid=%t matches=%t\n", c.row, ok, matched)
	}
}
