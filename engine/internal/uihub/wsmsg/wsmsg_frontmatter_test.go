package wsmsg_test

// TestFrontmatterParity guards against drift between wsmsg.go (the Go source
// of truth for the wire contract) and the hand-declared TS "frontmatter"
// block in tygo.yaml. wsmsg.go is listed in tygo.yaml's exclude_files, so
// `make gen-ts-check` (which only diffs the *generated* wsmsg.ts against its
// committed copy) can never notice that the frontmatter string drifted from
// wsmsg.go's actual consts/structs — the frontmatter is copied verbatim into
// the generated output regardless of what wsmsg.go says. This test reads
// tygo.yaml as plain text and cross-checks it against wsmsg.go's real Go
// values, so a rename/addition/removal on either side fails loudly here.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/earlisreal/eTape/engine/internal/uihub/wsmsg"
)

// tygoYAMLPath is relative to this package's directory, which is `go test`'s
// CWD.
const tygoYAMLPath = "../../../tygo.yaml"

// wsmsgGoPath is the sibling source file within this same package directory.
const wsmsgGoPath = "wsmsg.go"

// frontmatterText loads tygo.yaml and returns everything after the
// `frontmatter: |` marker — the hand-declared TS block. It's a plain
// substring slice (no YAML parsing) since the frontmatter is the last key in
// the file's only package entry; the marker only occurs once.
func frontmatterText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(tygoYAMLPath)
	if err != nil {
		t.Fatalf("reading %s: %v", tygoYAMLPath, err)
	}
	const marker = "frontmatter: |"
	idx := strings.Index(string(raw), marker)
	if idx == -1 {
		t.Fatalf("%s: could not find %q marker", tygoYAMLPath, marker)
	}
	return string(raw)[idx+len(marker):]
}

// extractUnionBody returns the text between `export type <name> =` and its
// terminating `;`, scoped so that enums sharing literal values (e.g. Side
// and TickDirection both have "BUY"/"SELL") can't cross-contaminate each
// other's checks.
func extractUnionBody(t *testing.T, frontmatter, name string) string {
	t.Helper()
	marker := "export type " + name + " ="
	idx := strings.Index(frontmatter, marker)
	if idx == -1 {
		t.Fatalf("frontmatter: could not find %q declaration", marker)
	}
	rest := frontmatter[idx+len(marker):]
	end := strings.Index(rest, ";")
	if end == -1 {
		t.Fatalf("frontmatter: %q declaration has no terminating ';'", marker)
	}
	return rest[:end]
}

var quotedLiteralRE = regexp.MustCompile(`"([^"]*)"`)

// quotedLiterals returns every double-quoted string literal in s, in order.
func quotedLiterals(s string) []string {
	matches := quotedLiteralRE.FindAllStringSubmatch(s, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// TestFrontmatterParity guards against drift between wsmsg.go's real Go
// consts/structs and the hand-declared TS frontmatter block in tygo.yaml, in
// three areas: topics, wire enums, and envelope kind discriminants.
func TestFrontmatterParity(t *testing.T) {
	t.Run("Topics", testFrontmatterParityTopics)
	t.Run("Enums", testFrontmatterParityEnums)
	t.Run("Kinds", testFrontmatterParityKinds)
}

// testFrontmatterParityTopics checks the Topic union bidirectionally against
// wsmsg.AllTopics, the real allow-list map, so neither side can drift without
// hand-maintaining a duplicate topic list in the test itself.
func testFrontmatterParityTopics(t *testing.T) {
	frontmatter := frontmatterText(t)
	topicBody := extractUnionBody(t, frontmatter, "Topic")

	// Forward: every topic the server recognizes must appear quoted in the
	// frontmatter's Topic union.
	for topic := range wsmsg.AllTopics {
		want := `"` + string(topic) + `"`
		if !strings.Contains(topicBody, want) {
			t.Errorf("wsmsg.AllTopics has %s, but frontmatter Topic union is missing %s", topic, want)
		}
	}

	// Reverse: every quoted literal in the frontmatter's Topic union must be
	// a real, recognized topic.
	for _, lit := range quotedLiterals(topicBody) {
		if !wsmsg.AllTopics[wsmsg.Topic(lit)] {
			t.Errorf("frontmatter Topic union has %q, but it is not a key in wsmsg.AllTopics", lit)
		}
	}
}

// testFrontmatterParityEnums checks the curated wire enums against the real
// typed Go consts. Building the curated slices from `string(wsmsg.SideBuy)`
// etc. (rather than literal strings) means a Go-side rename fails to
// compile, forcing this test to be updated in lockstep.
func testFrontmatterParityEnums(t *testing.T) {
	frontmatter := frontmatterText(t)

	enums := map[string][]string{
		"Side":          {string(wsmsg.SideBuy), string(wsmsg.SideSell), string(wsmsg.SideShort), string(wsmsg.SideCover)},
		"OrderType":     {string(wsmsg.OrderMarket), string(wsmsg.OrderLimit), string(wsmsg.OrderStop), string(wsmsg.OrderStopLimit)},
		"TIF":           {string(wsmsg.TIFDay), string(wsmsg.TIFGTC), string(wsmsg.TIFIOC), string(wsmsg.TIFFOK)},
		"OrderSession":  {string(wsmsg.SessionAuto), string(wsmsg.SessionRTH), string(wsmsg.SessionExtended), string(wsmsg.SessionOvernight)},
		"OrderStatus": {
			string(wsmsg.StatusSubmitted), string(wsmsg.StatusAccepted), string(wsmsg.StatusPartiallyFilled),
			string(wsmsg.StatusFilled), string(wsmsg.StatusCanceled), string(wsmsg.StatusRejected),
			string(wsmsg.StatusExpired), string(wsmsg.StatusBlocked), string(wsmsg.StatusReplaced),
		},
		"TickDirection": {string(wsmsg.DirBuy), string(wsmsg.DirSell), string(wsmsg.DirNeutral)},
		"Broker":        {string(wsmsg.BrokerTradeZero), string(wsmsg.BrokerAlpaca), string(wsmsg.BrokerMoomoo), string(wsmsg.BrokerSim)},
		"AckStatus":     {string(wsmsg.AckAccepted), string(wsmsg.AckBlocked)},
		"LinkName": {
			string(wsmsg.LinkUIEngine), string(wsmsg.LinkEngineMoomoo),
			string(wsmsg.LinkEngineTZ), string(wsmsg.LinkEngineAlpaca),
		},
		"LinkStatus": {string(wsmsg.LinkOK), string(wsmsg.LinkDegraded), string(wsmsg.LinkDown)},
	}

	for typeName, values := range enums {
		body := extractUnionBody(t, frontmatter, typeName)
		for _, v := range values {
			want := `"` + v + `"`
			if !strings.Contains(body, want) {
				t.Errorf("wsmsg.%s const %s has no matching %s in frontmatter's %q union", typeName, v, want, typeName)
			}
		}
	}

	// Hardening: count const specs per enum type in wsmsg.go via go/ast and
	// compare against the curated slice lengths above. Without this, a new
	// const added to an existing enum type (e.g. a 5th Side value) without
	// updating both the frontmatter and this test's curated list would pass
	// silently, since the curated-values-present check only looks for what
	// IS in the curated list, never for what's missing from it.
	counts := countConstsByType(t, wsmsgGoPath)
	for typeName, values := range enums {
		if got, want := counts[typeName], len(values); got != want {
			t.Errorf("wsmsg.go declares %d %s consts, but the curated enum list in this test has %d — "+
				"update both the curated list here and the frontmatter in tygo.yaml", got, typeName, want)
		}
	}
}

// countConstsByType parses filename and counts, per declared type name, how
// many const ValueSpecs are explicitly typed with that name (e.g.
// `SideBuy Side = "BUY"` increments counts["Side"]).
func countConstsByType(t *testing.T, filename string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", filename, err)
	}
	counts := make(map[string]int)
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || vs.Type == nil {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			counts[ident.Name] += len(vs.Names)
		}
	}
	return counts
}

// testFrontmatterParityKinds checks the 10 envelope `kind` discriminants
// (verified against wsmsg.go's *Msg struct doc comments/field literals) and
// that the frontmatter declares exactly those 10 interfaces, correctly
// partitioned into ServerMessage/ClientMessage.
func testFrontmatterParityKinds(t *testing.T) {
	frontmatter := frontmatterText(t)

	// kind -> interface name, verified against wsmsg.go's *Msg structs
	// (SnapshotMsg/DeltaMsg/AckMsg/PongMsg/ResultMsg = server->client;
	// SubscribeMsg/UnsubscribeMsg/CommandMsg/QueryMsg/PingMsg = client->server).
	kindToInterface := map[string]string{
		"snapshot":    "SnapshotMsg",
		"delta":       "DeltaMsg",
		"ack":         "AckMsg",
		"pong":        "PongMsg",
		"result":      "ResultMsg",
		"subscribe":   "SubscribeMsg",
		"unsubscribe": "UnsubscribeMsg",
		"command":     "CommandMsg",
		"query":       "QueryMsg",
		"ping":        "PingMsg",
	}
	serverMessage := []string{"SnapshotMsg", "DeltaMsg", "AckMsg", "PongMsg", "ResultMsg"}
	clientMessage := []string{"SubscribeMsg", "UnsubscribeMsg", "CommandMsg", "QueryMsg", "PingMsg"}

	if len(kindToInterface) != 10 {
		t.Fatalf("curated kindToInterface must have exactly 10 entries, has %d", len(kindToInterface))
	}

	for kind, iface := range kindToInterface {
		want := `kind: "` + kind + `"`
		if !strings.Contains(frontmatter, want) {
			t.Errorf("frontmatter missing %s (for interface %s)", want, iface)
		}
	}

	// Exactly 10 "...Msg" interfaces declared.
	ifaceRE := regexp.MustCompile(`export interface (\w+Msg) \{`)
	declared := ifaceRE.FindAllStringSubmatch(frontmatter, -1)
	if len(declared) != 10 {
		names := make([]string, 0, len(declared))
		for _, m := range declared {
			names = append(names, m[1])
		}
		t.Errorf("frontmatter declares %d '...Msg' interfaces (want 10): %v", len(declared), names)
	}
	declaredSet := make(map[string]bool, len(declared))
	for _, m := range declared {
		declaredSet[m[1]] = true
	}
	for _, iface := range kindToInterface {
		if !declaredSet[iface] {
			t.Errorf("frontmatter does not declare interface %s (expected for one of the 10 curated kinds)", iface)
		}
	}

	// ServerMessage / ClientMessage must list EXACTLY the right members: no
	// extra members, and no cross-listed duplicate (e.g. SnapshotMsg
	// accidentally appearing in ClientMessage too). A presence-only check
	// (substring/word match) would miss that, since it never verifies the
	// union doesn't ALSO contain something it shouldn't.
	serverBody := extractUnionBody(t, frontmatter, "ServerMessage")
	checkExactUnionMembers(t, "ServerMessage", serverBody, serverMessage)
	clientBody := extractUnionBody(t, frontmatter, "ClientMessage")
	checkExactUnionMembers(t, "ClientMessage", clientBody, clientMessage)
}

// checkExactUnionMembers splits body (a union's text between `=` and `;`)
// on `|`, trims whitespace from each piece, and verifies the resulting set
// of members is exactly equal to want — same size, same elements. Any
// member present in body but not in want (an unexpected/cross-listed
// member) and any member in want but missing from body are each reported
// by name, so a tygo.yaml edit that adds an extra or wrong member to a
// union fails loudly instead of silently passing a presence-only check.
func checkExactUnionMembers(t *testing.T, unionName, body string, want []string) {
	t.Helper()
	wantSet := make(map[string]bool, len(want))
	for _, w := range want {
		wantSet[w] = true
	}
	gotSet := make(map[string]bool)
	for _, part := range strings.Split(body, "|") {
		member := strings.TrimSpace(part)
		if member == "" {
			continue
		}
		gotSet[member] = true
	}
	for member := range gotSet {
		if !wantSet[member] {
			t.Errorf("frontmatter's %s union has unexpected member %q (expected exactly %v)", unionName, member, want)
		}
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("frontmatter's %s union is missing expected member %q", unionName, w)
		}
	}
}
