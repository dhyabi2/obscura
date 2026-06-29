package layering

import (
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// CoreClosure is the authoritative consensus core: the exact transitive
// import closure of pkg/chain within module "obscura". It changes ONLY via a
// deliberate protocol fork (and a matching edit to docs/PROJECT_ARCHITECTURE.md).
var CoreClosure = map[string]bool{
	"obscura/pkg/accumulator": true,
	"obscura/pkg/base58":      true,
	"obscura/pkg/block":       true,
	"obscura/pkg/chain":       true,
	"obscura/pkg/commit":      true,
	"obscura/pkg/config":      true,
	"obscura/pkg/consensus":   true,
	"obscura/pkg/fee":         true,
	"obscura/pkg/group":       true,
	"obscura/pkg/pow":         true,
	"obscura/pkg/pqaccum":     true,
	"obscura/pkg/pqsign":      true,
	"obscura/pkg/pqstealth":   true,
	"obscura/pkg/stark":       true,
	"obscura/pkg/swap":        true,
	"obscura/pkg/tx":          true,
}

// TestCoreIsClosed asserts that pkg/chain's transitive (non-test) import
// closure is EXACTLY the 16-package consensus core, no more, no less. A new
// import that pulls a peripheral package into the core fails here.
func TestCoreIsClosed(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "obscura/pkg/chain").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	got := map[string]bool{}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "obscura/") { // module-internal only
			got[p] = true
		}
	}
	var extra, missing []string
	for p := range got {
		if !CoreClosure[p] {
			extra = append(extra, p)
		}
	}
	for p := range CoreClosure {
		if !got[p] {
			missing = append(missing, p)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	if len(extra) > 0 {
		t.Fatalf("consensus core LEAKED upward, peripheral packages entered "+
			"pkg/chain's closure: %v\nIf this is an intentional fork, update CoreClosure "+
			"AND docs/PROJECT_ARCHITECTURE.md in the same change.", extra)
	}
	if len(missing) > 0 {
		t.Fatalf("core shrank, expected packages no longer in closure: %v "+
			"(intentional? update CoreClosure + docs).", missing)
	}
}
